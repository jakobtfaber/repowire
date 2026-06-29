package peer

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/repowire/repowire/daemon-go/proto"
)

// Liveness probes whether a recorded agent process is still running. Injected so
// the registry stays testable; the production impl in main shells out to the OS
// (syscall.Kill(pid, 0)).
type Liveness interface {
	PIDAlive(pid int) bool
}

// Transport is the subset of the WebSocket hub the registry needs to reconcile
// liveness and sever a retired peer's socket. Injected; the hub implements it.
type Transport interface {
	IsConnected(proto.PeerID) bool
	Close(proto.PeerID) error
}

// AllocateParams carries everything allocate_and_register needs. Identity-
// sensitive routing only ever flows through proto.PeerID (ClaimedPeerID); the
// human-facing DisplayName is derived, never an input key for routing.
type AllocateParams struct {
	Circle        string
	Backend       proto.AgentType
	Model         *string
	Path          *string
	PaneID        *string
	TmuxSession   *string
	Machine       string
	Role          proto.PeerRole
	ClaimedPeerID *proto.PeerID
	Metadata      map[string]any
	AgentPID      *int
}

// ErrPeerRetired is returned by AllocateAndRegister when a claim names a retired
// peer_id without proof of a live agent — an orphan ws-hook trying to resurrect
// a terminally-offlined identity.
var ErrPeerRetired = fmt.Errorf("peer: retired peer_id cannot be reclaimed without a live agent")

// peerState pairs the wire-facing proto.Peer with its authoritative lifecycle
// state. The FSM state is the source of truth; proto.Peer.Status is a projection
// kept in lockstep via Apply -> ToStatus, never assigned independently.
type peerState struct {
	peer  *proto.Peer
	state LifecycleState
}

// Registry is the lifecycle heart: peer state keyed by PeerID, a separate
// DisplayName index for addressing, durable mappings, retirement records, and
// demand-driven lazy_repair. All routing-sensitive lookups use PeerID.
type Registry struct {
	mu       sync.RWMutex
	peers    map[proto.PeerID]*peerState
	mappings map[proto.PeerID]*proto.SessionMapping
	retired  map[proto.PeerID]time.Time

	store     Store
	live      Liveness
	transport Transport

	repairMu   sync.Mutex
	lastRepair time.Time

	retiredTTL time.Duration
	reapTTL    time.Duration

	// OnOffline is a hook the hub sets so a terminal/transport offline can
	// cascade query cancellation (the hub owns the QueryTracker). The registry
	// stays query-agnostic. Called with the registry lock released.
	OnOffline func(proto.PeerID)
}

const (
	defaultRetiredTTL = 72 * time.Hour
	defaultReapTTL    = 30 * time.Minute // spike value; config later
	repairDebounce    = 30 * time.Second
)

// NewRegistry hydrates the in-memory state from the Store: live mappings plus
// retirement records still inside the TTL window.
func NewRegistry(ctx context.Context, store Store, live Liveness, transport Transport) (*Registry, error) {
	r := &Registry{
		peers:      make(map[proto.PeerID]*peerState),
		mappings:   make(map[proto.PeerID]*proto.SessionMapping),
		retired:    make(map[proto.PeerID]time.Time),
		store:      store,
		live:       live,
		transport:  transport,
		retiredTTL: defaultRetiredTTL,
		reapTTL:    defaultReapTTL,
	}

	mappings, err := store.LoadMappings(ctx)
	if err != nil {
		return nil, fmt.Errorf("load mappings: %w", err)
	}
	for _, m := range mappings {
		r.mappings[m.SessionID] = m
	}

	cutoff := time.Now().UTC().Add(-r.retiredTTL)
	retired, err := store.LoadRetired(ctx, cutoff)
	if err != nil {
		return nil, fmt.Errorf("load retired: %w", err)
	}
	for id, at := range retired {
		r.retired[id] = at
	}
	return r, nil
}

// AllocateAndRegister allocates (or reclaims) a peer identity and registers it
// ONLINE. Returns the canonical PeerID and the assigned DisplayName.
func (r *Registry) AllocateAndRegister(ctx context.Context, params AllocateParams) (proto.PeerID, proto.DisplayName, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// (a) Retirement guard. A claim naming a retired peer_id is an orphan ws-hook
	// reconnect unless it proves a live agent. Checked against `retired` (not
	// `peers`) so it covers ids already evicted from the registry.
	if params.ClaimedPeerID != nil {
		if _, isRetired := r.retired[*params.ClaimedPeerID]; isRetired {
			if params.AgentPID == nil || !r.live.PIDAlive(*params.AgentPID) {
				return "", "", ErrPeerRetired
			}
			r.unretireLocked(ctx, *params.ClaimedPeerID)
		}
	}

	now := time.Now().UTC()

	// (b) Name-collision reclaim: reuse an existing identity when the runtime
	// peer_id matches, or an existing peer holding the target display_name is
	// Offline (clean takeover).
	displayName := r.buildDisplayName(params)

	var id proto.PeerID
	if params.ClaimedPeerID != nil {
		if _, ok := r.peers[*params.ClaimedPeerID]; ok {
			id = *params.ClaimedPeerID
		} else if _, ok := r.mappings[*params.ClaimedPeerID]; ok {
			id = *params.ClaimedPeerID
		}
	}
	if id == "" {
		if reclaimed, ok := r.reclaimableOfflineLocked(displayName, params.Circle, params.Backend); ok {
			id = reclaimed
		}
	}

	// (c) Mint a fresh peer_id when not reclaiming.
	if id == "" {
		id = proto.PeerID(fmt.Sprintf("repow-%s-%s", params.Circle, uuid.NewString()[:8]))
	}

	// (d) Drive Unregistered --Connect--> Online through the FSM.
	next, err := Apply(StateUnregistered, EventConnect)
	if err != nil {
		// Unreachable for a well-formed FSM; fail loud rather than paper over.
		r.emitContradiction(ctx, id, displayName, "Unregistered", EventConnect)
		return "", "", fmt.Errorf("allocate: %w", err)
	}
	status, _ := next.ToStatus()

	p := &proto.Peer{
		PeerID:      id,
		DisplayName: displayName,
		Backend:     params.Backend,
		Circle:      params.Circle,
		Role:        params.Role,
		Status:      status,
		Model:       params.Model,
		PaneID:      params.PaneID,
		TmuxSession: params.TmuxSession,
		Machine:     params.Machine,
		Metadata:    params.Metadata,
		AgentPID:    params.AgentPID,
		LastSeen:    &now,
	}
	if params.Path != nil {
		p.Path = *params.Path
	}
	r.peers[id] = &peerState{peer: p, state: next}

	// Persist mapping in-memory; disk flush is deferred to lazy_repair.
	r.mappings[id] = &proto.SessionMapping{
		SessionID:   id,
		DisplayName: displayName,
		Circle:      params.Circle,
		Backend:     params.Backend,
		Path:        params.Path,
		Role:        params.Role,
		UpdatedAt:   now,
		Model:       params.Model,
		AgentPID:    params.AgentPID,
	}

	r.appendEvent(ctx, Event{Type: "peer_online", Timestamp: now, PeerID: id, PeerName: displayName, SessionID: id})
	return id, displayName, nil
}

// buildDisplayName derives the addressing name. Reclaim decides identity reuse;
// here we just compute the human-facing name (folder-backend). Must hold lock.
func (r *Registry) buildDisplayName(params AllocateParams) proto.DisplayName {
	folder := "peer"
	if params.Path != nil && *params.Path != "" {
		folder = baseFolder(*params.Path)
	}
	return proto.DisplayName(fmt.Sprintf("%s-%s", folder, params.Backend))
}

// reclaimableOfflineLocked returns a PeerID whose Offline peer currently holds
// the given (display_name, circle, backend) — a clean takeover candidate.
func (r *Registry) reclaimableOfflineLocked(name proto.DisplayName, circle string, backend proto.AgentType) (proto.PeerID, bool) {
	for id, ps := range r.peers {
		if ps.peer.DisplayName == name &&
			ps.peer.Circle == circle &&
			ps.peer.Backend == backend &&
			ps.state == StateOffline {
			return id, true
		}
	}
	return "", false
}

// MarkOffline drives the peer offline. terminal=true retires the identity, severs
// its transport, and records the retirement so an orphan ws-hook cannot
// resurrect it. Returns the number of cancelled queries (via OnOffline).
func (r *Registry) MarkOffline(ctx context.Context, id proto.PeerID, terminal bool) (int, error) {
	r.mu.Lock()
	ps, ok := r.peers[id]
	if !ok {
		// Terminal offline for an id already evicted must still retire it, or the
		// orphan it came from could re-register through a persisted mapping.
		if terminal {
			r.retireLocked(ctx, id)
		}
		r.mu.Unlock()
		return 0, nil
	}

	event := EventTransportDisconnect
	if terminal {
		event = EventTerminalOffline
	}
	next, err := Apply(ps.state, event)
	if err != nil {
		r.emitContradiction(ctx, id, ps.peer.DisplayName, ps.state, event)
		r.mu.Unlock()
		return 0, nil // fail loud (event emitted), leave state unchanged
	}
	now := time.Now().UTC()
	ps.state = next
	if status, ok := next.ToStatus(); ok {
		ps.peer.Status = status
	}
	ps.peer.LastSeen = &now

	if terminal {
		r.retireLocked(ctx, id)
	}
	name := ps.peer.DisplayName
	r.mu.Unlock()

	if terminal && r.transport != nil {
		_ = r.transport.Close(id)
	}
	evType := "peer_offline"
	r.appendEvent(ctx, Event{Type: evType, Timestamp: now, PeerID: id, PeerName: name, SessionID: id})

	cancelled := 0
	if r.OnOffline != nil {
		r.OnOffline(id)
	}
	return cancelled, nil
}

// UpdateStatus applies a wire status frame through the FSM (e.g. a Stop hook
// reporting Online, a UserPromptSubmit reporting Busy). The status frame names a
// target PeerStatus; we translate it to the matching lifecycle event so illegal
// moves still fail loud.
func (r *Registry) UpdateStatus(ctx context.Context, id proto.PeerID, status proto.PeerStatus) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	ps, ok := r.peers[id]
	if !ok {
		return nil
	}
	event, ok := statusToEvent(status)
	if !ok {
		return nil
	}
	next, err := Apply(ps.state, event)
	if err != nil {
		r.emitContradiction(ctx, id, ps.peer.DisplayName, ps.state, event)
		return nil
	}
	now := time.Now().UTC()
	ps.state = next
	if s, ok := next.ToStatus(); ok {
		ps.peer.Status = s
	}
	ps.peer.LastSeen = &now
	r.appendEvent(ctx, Event{Type: "peer_status", Timestamp: now, PeerID: id, PeerName: ps.peer.DisplayName, SessionID: id, Payload: map[string]any{"status": string(status)}})
	return nil
}

// statusToEvent maps a desired wire status onto the lifecycle event that reaches
// it from a live state. online->Stop (Busy->Online or Online->Online),
// busy->UserPromptSubmit, offline->TransportDisconnect.
func statusToEvent(status proto.PeerStatus) (LifecycleEvent, bool) {
	switch status {
	case proto.StatusOnline:
		return EventStop, true
	case proto.StatusBusy:
		return EventUserPromptSubmit, true
	case proto.StatusOffline:
		return EventTransportDisconnect, true
	}
	return "", false
}

// UpdateTurnState updates per-turn progress (orthogonal to lifecycle status).
func (r *Registry) UpdateTurnState(ctx context.Context, id proto.PeerID, ts proto.TurnState) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if ps, ok := r.peers[id]; ok {
		ps.peer.TurnState = ts
		now := time.Now().UTC()
		ps.peer.LastSeen = &now
	}
}

// SetCircle moves a peer between circles, keeping the durable mapping in sync —
// the stale-mapping bug fix: peer AND mapping under one lock.
func (r *Registry) SetCircle(ctx context.Context, id proto.PeerID, circle string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ps, ok := r.peers[id]
	if !ok {
		return
	}
	ps.peer.Circle = circle
	if m, ok := r.mappings[id]; ok {
		m.Circle = circle
		m.UpdatedAt = time.Now().UTC()
	}
}

// UpdateDisplayName renames a peer in place, preserving PeerID and keeping the
// mapping in sync. Evicts Offline ghosts holding the same (name, backend);
// returns false if a live peer already holds the name.
func (r *Registry) UpdateDisplayName(ctx context.Context, id proto.PeerID, name proto.DisplayName) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ps, ok := r.peers[id]
	if !ok {
		return false, nil
	}
	var toEvict []proto.PeerID
	for otherID, other := range r.peers {
		if otherID == id || other.peer.DisplayName != name || other.peer.Backend != ps.peer.Backend {
			continue
		}
		if other.state == StateOffline {
			toEvict = append(toEvict, otherID)
		} else {
			return false, nil
		}
	}
	for _, e := range toEvict {
		delete(r.peers, e)
	}
	ps.peer.DisplayName = name
	if m, ok := r.mappings[id]; ok {
		m.DisplayName = name
		m.UpdatedAt = time.Now().UTC()
	}
	return true, nil
}

// GetPeer returns a peer by PeerID. Routing-sensitive callers MUST hold a
// PeerID; the compiler refuses a DisplayName here.
func (r *Registry) GetPeer(id proto.PeerID) (*proto.Peer, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ps, ok := r.peers[id]
	if !ok {
		return nil, false
	}
	return ps.peer, true
}

// GetPeersByCircle returns all peers in a circle.
func (r *Registry) GetPeersByCircle(circle string) []*proto.Peer {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []*proto.Peer
	for _, ps := range r.peers {
		if ps.peer.Circle == circle {
			out = append(out, ps.peer)
		}
	}
	return out
}

// LazyRepair is demand-driven maintenance: demote ghosts, reap dangling offline
// peers, persist mappings, prune expired retirements. Debounced to ~1x/30s and
// never run on a timer. All status changes go through Apply; an illegal move
// emits a contradiction and leaves state unchanged.
func (r *Registry) LazyRepair(ctx context.Context) {
	if time.Since(r.lastRepair) < repairDebounce {
		return
	}
	if !r.repairMu.TryLock() {
		return
	}
	defer r.repairMu.Unlock()
	if time.Since(r.lastRepair) < repairDebounce {
		return
	}
	r.lastRepair = time.Now()

	r.demoteDisconnected(ctx)
	r.reapDangling(ctx)
	r.persistMappings(ctx)
	r.pruneRetired(ctx)
}

// demoteDisconnected marks Online/Busy peers OFFLINE when they have no live
// WebSocket and no live agent process (a ghost).
func (r *Registry) demoteDisconnected(ctx context.Context) {
	r.mu.Lock()
	var ghosts []proto.PeerID
	for id, ps := range r.peers {
		if ps.state != StateOnline && ps.state != StateBusy {
			continue
		}
		if r.transport != nil && r.transport.IsConnected(id) {
			continue
		}
		if ps.peer.AgentPID != nil && r.live.PIDAlive(*ps.peer.AgentPID) {
			continue
		}
		ghosts = append(ghosts, id)
	}
	now := time.Now().UTC()
	type demoted struct {
		id   proto.PeerID
		name proto.DisplayName
	}
	var done []demoted
	for _, id := range ghosts {
		ps := r.peers[id]
		next, err := Apply(ps.state, EventGhostDemote)
		if err != nil {
			r.emitContradictionLocked(ctx, id, ps.peer.DisplayName, ps.state, EventGhostDemote)
			continue
		}
		ps.state = next
		if s, ok := next.ToStatus(); ok {
			ps.peer.Status = s
		}
		ps.peer.LastSeen = &now
		done = append(done, demoted{id, ps.peer.DisplayName})
	}
	r.mu.Unlock()

	for _, d := range done {
		r.appendEvent(ctx, Event{Type: "peer_offline", Timestamp: now, PeerID: d.id, PeerName: d.name, SessionID: d.id,
			Payload: map[string]any{"reason": "online_but_no_ws"}})
	}
}

// reapDangling removes Offline peers past the reap TTL with no live agent.
func (r *Registry) reapDangling(ctx context.Context) {
	r.mu.Lock()
	cutoff := time.Now().UTC().Add(-r.reapTTL)
	var doomed []proto.PeerID
	for id, ps := range r.peers {
		if ps.state != StateOffline {
			continue
		}
		if ps.peer.LastSeen == nil || !ps.peer.LastSeen.Before(cutoff) {
			continue
		}
		if ps.peer.AgentPID != nil && r.live.PIDAlive(*ps.peer.AgentPID) {
			continue
		}
		doomed = append(doomed, id)
	}
	now := time.Now().UTC()
	type reaped struct {
		id   proto.PeerID
		name proto.DisplayName
	}
	var done []reaped
	for _, id := range doomed {
		ps := r.peers[id]
		next, err := Apply(ps.state, EventReap)
		if err != nil {
			r.emitContradictionLocked(ctx, id, ps.peer.DisplayName, ps.state, EventReap)
			continue
		}
		name := ps.peer.DisplayName
		_ = next // peer is being removed; Retired is its terminal state
		delete(r.peers, id)
		delete(r.mappings, id)
		r.retired[id] = now
		done = append(done, reaped{id, name})
	}
	r.mu.Unlock()

	for _, d := range done {
		_ = r.store.DeleteMapping(ctx, d.id)
		_ = r.store.Retire(ctx, d.id, now)
		r.appendEvent(ctx, Event{Type: "peer_reaped", Timestamp: now, PeerID: d.id, PeerName: d.name, SessionID: d.id,
			Payload: map[string]any{"reason": "offline_ttl"}})
	}
}

// persistMappings flushes every live mapping (deferred from mutation time).
func (r *Registry) persistMappings(ctx context.Context) {
	r.mu.RLock()
	snapshot := make([]*proto.SessionMapping, 0, len(r.mappings))
	for _, m := range r.mappings {
		cp := *m
		snapshot = append(snapshot, &cp)
	}
	r.mu.RUnlock()
	for _, m := range snapshot {
		_ = r.store.UpsertMapping(ctx, m)
	}
}

// pruneRetired drops retirement records older than the TTL.
func (r *Registry) pruneRetired(ctx context.Context) {
	cutoff := time.Now().UTC().Add(-r.retiredTTL)
	r.mu.Lock()
	var expired []proto.PeerID
	for id, at := range r.retired {
		if !at.After(cutoff) {
			expired = append(expired, id)
		}
	}
	for _, id := range expired {
		delete(r.retired, id)
	}
	r.mu.Unlock()
	for _, id := range expired {
		_ = r.store.Unretire(ctx, id)
	}
}

// --- retirement helpers (must hold lock) ---

func (r *Registry) retireLocked(ctx context.Context, id proto.PeerID) {
	at := time.Now().UTC()
	r.retired[id] = at
	_ = r.store.Retire(ctx, id, at)
}

func (r *Registry) unretireLocked(ctx context.Context, id proto.PeerID) {
	delete(r.retired, id)
	_ = r.store.Unretire(ctx, id)
}

// --- event/contradiction helpers ---

func (r *Registry) appendEvent(ctx context.Context, e Event) {
	if e.EventID == "" {
		e.EventID = uuid.NewString()
	}
	_ = r.store.AppendEvent(ctx, e)
}

// emitContradiction records a fail-loud peer_contradiction when an illegal
// transition is attempted. Safe to call without the lock held.
func (r *Registry) emitContradiction(ctx context.Context, id proto.PeerID, name proto.DisplayName, from LifecycleState, event LifecycleEvent) {
	r.appendEvent(ctx, Event{
		Type:      "peer_contradiction",
		Timestamp: time.Now().UTC(),
		PeerID:    id,
		PeerName:  name,
		SessionID: id,
		Payload: map[string]any{
			"from_state": string(from),
			"event":      string(event),
			"detail":     "illegal lifecycle transition rejected",
		},
	})
}

// emitContradictionLocked is emitContradiction usable while the lock is held: it
// records the same journal row (AppendEvent does not touch registry state).
func (r *Registry) emitContradictionLocked(ctx context.Context, id proto.PeerID, name proto.DisplayName, from LifecycleState, event LifecycleEvent) {
	r.emitContradiction(ctx, id, name, from, event)
}

// baseFolder returns the trailing path component (the folder name) of a path.
func baseFolder(path string) string {
	end := len(path)
	for end > 0 && path[end-1] == '/' {
		end--
	}
	start := end
	for start > 0 && path[start-1] != '/' {
		start--
	}
	if start == end {
		return "peer"
	}
	return path[start:end]
}
