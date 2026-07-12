package peer

import (
	"context"
	"time"

	"github.com/repowire/repowire/daemon-go/proto"
)

// registry_lifecycle_byname.go holds the addressing-string (peer_id OR
// display_name) wrappers the peer-lifecycle HTTP route group calls. The wire
// passes display_name; identity-sensitive state stays canonicalized to
// proto.PeerID — these wrappers resolve the string ONCE (fail-loud on ambiguity,
// mirroring _lookup_peer_unlocked) and then mutate keyed on the resolved PeerID.
// The PeerID-keyed primitives (MarkOffline(PeerID), SetCircle(PeerID), ...) stay
// in registry.go and are the source of truth; these never bypass the FSM.

// UnregisterPeer removes a peer (registry + durable mapping) addressed by peer_id
// or display_name, optionally scoped to a circle to disambiguate same-name peers.
// Returns (found, err); an ambiguous name (→ 409) is now a fail-loud error, same
// as every other by-name mutator in this file — this deliberately diverges from
// Python's unregister_peer, which silently takes the first match on the
// display_name scan. NON-terminal: it does NOT retire the identity (unlike a
// terminal MarkOffline) — a fresh SessionStart may legitimately reuse the name.
// The pane-adoption rollback path relies on exactly this (no retirement residue).
func (r *Registry) UnregisterPeer(ctx context.Context, identifier string, circle *string) (bool, error) {
	r.mu.Lock()
	// peer_id hit is unambiguous and matches Python's "try as session_id first".
	if ps, ok := r.peers[proto.PeerID(identifier)]; ok {
		delete(r.peers, ps.peer.PeerID)
		delete(r.mappings, ps.peer.PeerID)
		id := ps.peer.PeerID
		r.mu.Unlock()
		_ = r.store.DeleteMapping(ctx, id)
		return true, nil
	}
	p, err := r.resolvePeerLocked(identifier, circle)
	if err != nil {
		r.mu.Unlock()
		return false, err
	}
	if p == nil {
		r.mu.Unlock()
		return false, nil
	}
	id := p.PeerID
	delete(r.peers, id)
	delete(r.mappings, id)
	r.mu.Unlock()
	_ = r.store.DeleteMapping(ctx, id)
	return true, nil
}

// MarkOfflineByName resolves an addressing string to its canonical PeerID, then
// drives the PeerID-keyed MarkOffline (which owns the FSM transition + terminal
// retirement). Returns (found, cancelledQueries, err). A resolution ambiguity is
// a fail-loud error (→ 409); (false,0,nil) is "no such peer" (→ the route may
// still treat a terminal offline of an unknown id as a no-op, mirroring Python).
func (r *Registry) MarkOfflineByName(ctx context.Context, identifier string, terminal bool) (found bool, cancelled int, err error) {
	return r.MarkOfflineByNameWithReason(ctx, identifier, terminal, "terminal_offline")
}

// MarkOfflineByNameWithReason preserves the explicit terminal cause for
// durable-job failure and other terminal-offline observers.
func (r *Registry) MarkOfflineByNameWithReason(ctx context.Context, identifier string, terminal bool, reason string) (found bool, cancelled int, err error) {
	r.mu.RLock()
	p, rerr := r.resolvePeerLocked(identifier, nil)
	r.mu.RUnlock()
	if rerr != nil {
		return false, 0, rerr
	}
	if p == nil {
		// Terminal offline for an id already evicted must still retire it so an
		// orphan ws-hook cannot re-register through a persisted mapping. Only a
		// peer_id-shaped identifier can be retired (display names aren't identity).
		if terminal && looksLikePeerID(identifier) {
			c, mErr := r.MarkOfflineWithReason(ctx, proto.PeerID(identifier), true, reason)
			return false, c, mErr
		}
		return false, 0, nil
	}
	c, mErr := r.MarkOfflineWithReason(ctx, p.PeerID, terminal, reason)
	return true, c, mErr
}

// SetCircleByName resolves an addressing string and moves the peer to circle,
// keeping peer + durable mapping in sync under one lock. Returns true if found.
// Mirrors PeerRegistry.set_peer_circle (best-effort: unknown peer is a no-op).
func (r *Registry) SetCircleByName(ctx context.Context, identifier, circle string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, err := r.resolvePeerLocked(identifier, nil)
	if err != nil || p == nil {
		return false
	}
	p.Circle = circle
	if m, ok := r.mappings[p.PeerID]; ok {
		m.Circle = circle
		m.UpdatedAt = time.Now().UTC()
	}
	return true
}

// TouchLastSeen refreshes a peer's last_seen WITHOUT touching transport status.
// Outbound MCP traffic is process activity, not proof of inbound reachability —
// WebSocket connect/disconnect owns ONLINE/OFFLINE; touch only feeds last_seen-
// keyed liveness. Returns true if found; an ambiguous name is a fail-loud error
// (→ 409). Mirrors PeerRegistry.touch_last_seen.
func (r *Registry) TouchLastSeen(ctx context.Context, identifier string, circle *string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, err := r.resolvePeerLocked(identifier, circle)
	if err != nil {
		return false, err
	}
	if p == nil {
		return false, nil
	}
	now := time.Now().UTC()
	p.LastSeen = &now
	return true, nil
}

// UpdateDescription sets a peer's task description in live state + durable mapping.
// Returns (found, err); an ambiguous name is a fail-loud error (→ 409). Mirrors
// PeerRegistry.update_description.
func (r *Registry) UpdateDescription(ctx context.Context, identifier, description string, circle *string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, err := r.resolvePeerLocked(identifier, circle)
	if err != nil {
		return false, err
	}
	if p == nil {
		return false, nil
	}
	now := time.Now().UTC()
	p.Description = description
	if description == "" {
		delete(r.descriptionSetAt, p.PeerID)
	} else {
		r.descriptionSetAt[p.PeerID] = now
	}
	p.LastSeen = &now
	if m, ok := r.mappings[p.PeerID]; ok && m.Description != description {
		m.Description = description
		m.UpdatedAt = now
	}
	return true, nil
}

// looksLikePeerID reports whether an identifier is a canonical daemon-minted
// peer_id (repow-<circle>-<hex>) rather than a display_name. Mirrors the Python
// `identifier.startswith("repow-")` retirement guard in mark_offline.
func looksLikePeerID(identifier string) bool {
	return len(identifier) > 6 && identifier[:6] == "repow-"
}
