package peer

import (
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/repowire/repowire/daemon-go/proto"
)

// events.go adds the in-memory dashboard event window and the addressing-side
// peer lookups (by pane, by string identifier) that the HTTP routes/delivery
// layer call. The buffer mirrors the Python daemon.event_log.EventLog: a bounded
// (last 500) deque of wire dicts shaped {"id","type","timestamp",...data},
// gap-recoverable via events_since. Persistence stays in the Store (AppendEvent);
// this buffer is the live read surface only — it deliberately does NOT write to
// disk, because the Store is already the durable journal.

// eventsBufferCapacity bounds the in-memory window. Matches the Python EventLog
// max_events default (last 500).
const eventsBufferCapacity = 500

// eventBuffer is a bounded FIFO of event maps with a coarse mutex. Reads return
// snapshot copies of the slice (the maps themselves are not deep-copied; callers
// treat them as read-only, exactly like the Python list(self.events)).
type eventBuffer struct {
	mu     sync.Mutex
	events []map[string]any
}

// eventLog lazily initialises and returns the registry's in-memory event window,
// mirroring recOrZero so tests that never touch events pay nothing.
func (r *Registry) eventLog() *eventBuffer {
	// MUST NOT take r.mu: appendEvent (the primary caller) runs while r.mu is
	// already held, and r.mu is non-reentrant — guarding this init with r.mu
	// deadlocked AllocateAndRegister/MarkOffline. sync.Once gates the lazy-init
	// independently of the registry lock; the eventBuffer has its own mutex.
	r.evlogOnce.Do(func() {
		r.evlog = &eventBuffer{}
	})
	return r.evlog
}

// push appends a fully-formed event map, evicting the oldest beyond capacity.
func (b *eventBuffer) push(ev map[string]any) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = append(b.events, ev)
	if over := len(b.events) - eventsBufferCapacity; over > 0 {
		b.events = b.events[over:]
	}
}

// appendStructured records a lifecycle Event (the journal shape the registry
// emits) into the dashboard window using the same wire keys the Python EventLog
// produces, so a dashboard reading GET /events sees lifecycle and chat events in
// one stream. Only non-empty/non-zero fields are projected.
func (b *eventBuffer) appendStructured(e Event) {
	ev := map[string]any{
		"id":        e.EventID,
		"type":      e.Type,
		"timestamp": e.Timestamp.UTC().Format(time.RFC3339Nano),
	}
	if e.PeerID != "" {
		ev["peer_id"] = string(e.PeerID)
	}
	if e.PeerName != "" {
		ev["peer_name"] = string(e.PeerName)
	}
	if e.SessionID != "" {
		ev["session_id"] = string(e.SessionID)
	}
	for k, v := range e.Payload {
		ev[k] = v
	}
	b.push(ev)
}

// AddEvent records a dashboard event of the given type, merging data into the
// wire shape {"id","type","timestamp",...data} and returning the minted event id.
// Mirrors PeerRegistry.add_event / EventLog.add_event. Callers (the chat-ingest
// and query/response routes) pass already-wire-shaped data maps.
func (r *Registry) AddEvent(eventType string, data map[string]any) string {
	id := uuid.NewString()
	ev := map[string]any{
		"id":        id,
		"type":      eventType,
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
	}
	for k, v := range data {
		// id/type/timestamp from data must never shadow the canonical envelope.
		if k == "id" || k == "type" || k == "timestamp" {
			continue
		}
		ev[k] = v
	}
	r.eventLog().push(ev)
	return id
}

// GetEvents returns a snapshot of the full buffered window (last 500), oldest
// first. Mirrors PeerRegistry.get_events.
func (r *Registry) GetEvents() []map[string]any {
	b := r.eventLog()
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]map[string]any, len(b.events))
	copy(out, b.events)
	return out
}

// EventsSince returns events after the given id. If the id is empty or has been
// evicted from the buffer, it returns the full window (gap-recovery fallback).
// Mirrors PeerRegistry.events_since / EventLog.events_since.
func (r *Registry) EventsSince(eventID string) []map[string]any {
	b := r.eventLog()
	b.mu.Lock()
	defer b.mu.Unlock()
	if eventID == "" {
		out := make([]map[string]any, len(b.events))
		copy(out, b.events)
		return out
	}
	for i, ev := range b.events {
		if id, _ := ev["id"].(string); id == eventID {
			tail := b.events[i+1:]
			out := make([]map[string]any, len(tail))
			copy(out, tail)
			return out
		}
	}
	// Evicted id → gap-recovery: return everything we still hold.
	out := make([]map[string]any, len(b.events))
	copy(out, b.events)
	return out
}

// GetPeerByPane lives in registry.go (pre-existing). Not redeclared here.

// GetAllPeers returns every registered peer (live snapshot). Mirrors
// PeerRegistry.get_all_peers.
func (r *Registry) GetAllPeers() []*proto.Peer {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*proto.Peer, 0, len(r.peers))
	for _, ps := range r.peers {
		out = append(out, ps.peer)
	}
	return out
}

// ResolveByIdentifier resolves an addressing string that may be a canonical
// PeerID or a DisplayName. PeerID is tried first (exact identity match); falling
// back to DisplayName returns the most-recently-seen match so a stale offline
// ghost never shadows a live peer holding the reclaimed name. Returns (nil,false)
// when nothing matches; an ambiguous DisplayName is NOT an error here (chat
// ingest only needs a best-effort peer to scope the event), matching the
// best-effort get_peer the Python chat route uses.
func (r *Registry) ResolveByIdentifier(identifier string) (*proto.Peer, bool) {
	if identifier == "" {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if ps, ok := r.peers[proto.PeerID(identifier)]; ok {
		return ps.peer, true
	}
	var best *proto.Peer
	for _, ps := range r.peers {
		if string(ps.peer.DisplayName) != identifier {
			continue
		}
		if best == nil {
			best = ps.peer
			continue
		}
		if lastSeenAfter(ps.peer, best) {
			best = ps.peer
		}
	}
	return best, best != nil
}

// lastSeenAfter reports whether a was seen more recently than b (nil last_seen
// sorts oldest).
func lastSeenAfter(a, b *proto.Peer) bool {
	if a.LastSeen == nil {
		return false
	}
	if b.LastSeen == nil {
		return true
	}
	return a.LastSeen.After(*b.LastSeen)
}
