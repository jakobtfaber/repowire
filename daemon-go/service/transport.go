package service

// transport.go owns WebSocketTransport, the concrete per-connection socket
// registry that the router and delivery services send frames through. It
// implements peer.Transport (the registry's liveness/sever seam) so the same
// transport instance is both the registry's runtime-evidence source and the
// socket the hub package's /ws handler serves on.

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/repowire/repowire/daemon-go/peer"
	"github.com/repowire/repowire/daemon-go/proto"
)

// ErrNotConnected is returned by Send when the target peer has no live socket.
var ErrNotConnected = errors.New("hub: peer not connected")

// deliveryAckTimeout mirrors the Python transport's 0.75s best-effort window.
const deliveryAckTimeout = 750 * time.Millisecond

// pingTimeout mirrors the Python transport's default pong wait.
const pingTimeout = 5 * time.Second

// writeTimeout bounds each WebSocket write. The delivery ack timeout only starts
// after a write returns, so a wedged half-open socket needs its own deadline.
const writeTimeout = 10 * time.Second

// ConnectionInfo is the live socket record for one connected peer. SessionID IS
// the peer_id (the daemon-assigned identity), so it is typed proto.PeerID.
type ConnectionInfo struct {
	SessionID   proto.PeerID
	WS          *websocket.Conn
	PaneID      *string
	DisplayName proto.DisplayName
	ConnectedAt time.Time
}

// ackKey indexes an in-flight delivery ack by (peer, delivery id). Both halves
// matter: the same delivery_id can be reused across peers.
type ackKey struct {
	id         proto.PeerID
	deliveryID string
}

// WebSocketTransport owns every live socket and the in-flight pong/delivery-ack
// channels. All maps are guarded by mu. It implements peer.Transport so the
// registry can probe liveness and sever a retired peer's socket.
type WebSocketTransport struct {
	mu    sync.RWMutex
	conns map[proto.PeerID]*ConnectionInfo
	pongs map[proto.PeerID]chan map[string]any
	acks  map[ackKey]chan map[string]any
}

var _ peer.Transport = (*WebSocketTransport)(nil)

// NewWebSocketTransport returns an empty transport ready to accept connections.
func NewWebSocketTransport() *WebSocketTransport {
	return &WebSocketTransport{
		conns: make(map[proto.PeerID]*ConnectionInfo),
		pongs: make(map[proto.PeerID]chan map[string]any),
		acks:  make(map[ackKey]chan map[string]any),
	}
}

// Connect stores a fresh connection. If a connection already exists for the id
// it is closed first — a reconnect must not silently leak the old socket.
func (t *WebSocketTransport) Connect(ctx context.Context, info *ConnectionInfo) {
	t.mu.Lock()
	old := t.conns[info.SessionID]
	t.conns[info.SessionID] = info
	t.mu.Unlock()

	if old != nil && old.WS != nil {
		_ = old.WS.Close(websocket.StatusNormalClosure, "superseded by reconnect")
	}
}

// Disconnect removes the connection ONLY if the stored socket is the same one
// the caller holds. This is the guard for the old-handler-removing-the-new-
// connection race: a stale ws goroutine tearing down must not evict the fresh
// reconnect that already took its place. Returns true when it actually removed.
func (t *WebSocketTransport) Disconnect(ctx context.Context, id proto.PeerID, ws *websocket.Conn) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	info, ok := t.conns[id]
	if !ok {
		return false
	}
	if info.WS != ws {
		// A newer connection replaced this one; the stale handler must not evict it.
		return false
	}
	delete(t.conns, id)
	return true
}

// Send writes a frame to the peer's socket as JSON.
func (t *WebSocketTransport) Send(ctx context.Context, id proto.PeerID, v any) error {
	t.mu.RLock()
	info, ok := t.conns[id]
	t.mu.RUnlock()
	if !ok {
		return fmt.Errorf("%w: %s", ErrNotConnected, id)
	}
	writeCtx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	return wsjson.Write(writeCtx, info.WS, v)
}

// SendAndWaitDeliveryAck sends a frame carrying a delivery_id and waits up to
// timeout for the matching delivery_ack. Best-effort: a timeout returns
// (nil, nil) rather than an error, since older hooks never ack. A frame without
// a string delivery_id is sent as a plain Send (no ack channel registered).
func (t *WebSocketTransport) SendAndWaitDeliveryAck(ctx context.Context, id proto.PeerID, v any, timeout time.Duration) (map[string]any, error) {
	deliveryID, ok := extractDeliveryID(v)
	if !ok || deliveryID == "" {
		return nil, t.Send(ctx, id, v)
	}
	if timeout <= 0 {
		timeout = deliveryAckTimeout
	}

	key := ackKey{id: id, deliveryID: deliveryID}
	ch := make(chan map[string]any, 1)
	t.mu.Lock()
	t.acks[key] = ch
	t.mu.Unlock()
	defer func() {
		t.mu.Lock()
		delete(t.acks, key)
		t.mu.Unlock()
	}()

	if err := t.Send(ctx, id, v); err != nil {
		return nil, err
	}

	select {
	case data := <-ch:
		return data, nil
	case <-time.After(timeout):
		return nil, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// ResolveDeliveryAck hands a delivery_ack frame to whoever is waiting on it.
func (t *WebSocketTransport) ResolveDeliveryAck(id proto.PeerID, deliveryID string, data map[string]any) {
	if deliveryID == "" {
		return
	}
	t.mu.Lock()
	ch, ok := t.acks[ackKey{id: id, deliveryID: deliveryID}]
	if ok {
		delete(t.acks, ackKey{id: id, deliveryID: deliveryID})
	}
	t.mu.Unlock()
	if ok {
		ch <- data
	}
}

// Ping sends a ping frame and waits up to timeout for the matching pong.
func (t *WebSocketTransport) Ping(ctx context.Context, id proto.PeerID, timeout time.Duration) (map[string]any, error) {
	if timeout <= 0 {
		timeout = pingTimeout
	}
	ch := make(chan map[string]any, 1)
	t.mu.Lock()
	if _, exists := t.conns[id]; !exists {
		t.mu.Unlock()
		return nil, fmt.Errorf("%w: %s", ErrNotConnected, id)
	}
	t.pongs[id] = ch
	t.mu.Unlock()
	defer func() {
		t.mu.Lock()
		delete(t.pongs, id)
		t.mu.Unlock()
	}()

	if err := t.Send(ctx, id, map[string]any{"type": string(proto.FramePing)}); err != nil {
		return nil, err
	}
	select {
	case data := <-ch:
		return data, nil
	case <-time.After(timeout):
		return nil, fmt.Errorf("hub: ping timeout for %s", id)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// ResolvePong hands a pong frame to whoever is waiting on Ping.
func (t *WebSocketTransport) ResolvePong(id proto.PeerID, data map[string]any) {
	t.mu.Lock()
	ch, ok := t.pongs[id]
	if ok {
		delete(t.pongs, id)
	}
	t.mu.Unlock()
	if ok {
		ch <- data
	}
}

// ConnectionPaneID returns the pane_id recorded on the live connection, for
// truthful delivery-trace logging. ("", false) when not connected (or no pane).
// Mirrors the Python transport.get_connection_pane_id used in the delivery
// trace log line.
func (t *WebSocketTransport) ConnectionPaneID(id proto.PeerID) (string, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	info, ok := t.conns[id]
	if !ok || info.PaneID == nil {
		return "", false
	}
	return *info.PaneID, true
}

// ACPRoute reports whether this target should route over ACP instead of WS.
//
// ponytail: STUB. Always returns (nil, false) so every target falls through to
// the WS path. Upgrade path: when the ACP broker experiment lands, replace this
// with an AcpRouter implementation that mirrors PeerTransportRouter.acp_route
// (maybe_decide_acp_route gated on experiments.acp_broker_client) and returns a
// non-nil *ACPRouteDecision for brokered peers. The router/delivery/reconcile
// code already branches on the bool, so no signature churn is needed then.
func (t *WebSocketTransport) ACPRoute(target *proto.Peer) (*ACPRouteDecision, bool) {
	return nil, false
}

// IsConnected reports whether the peer has a live socket. Used by the registry's
// ghost-eviction pass.
func (t *WebSocketTransport) IsConnected(id proto.PeerID) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	_, ok := t.conns[id]
	return ok
}

// GetAllSessions returns the peer_ids of every connected peer.
func (t *WebSocketTransport) GetAllSessions() []proto.PeerID {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]proto.PeerID, 0, len(t.conns))
	for id := range t.conns {
		out = append(out, id)
	}
	return out
}

// Close severs a retired peer's socket (peer.Transport). It removes the record
// under lock and closes the underlying connection. Idempotent.
func (t *WebSocketTransport) Close(id proto.PeerID) error {
	t.mu.Lock()
	info, ok := t.conns[id]
	if ok {
		delete(t.conns, id)
	}
	t.mu.Unlock()
	if ok && info.WS != nil {
		return info.WS.Close(websocket.StatusGoingAway, "peer retired")
	}
	return nil
}

// extractDeliveryID pulls the delivery_id out of an outbound frame regardless of
// whether it's a typed *proto.QueryFrame, a map, or carries no id at all.
func extractDeliveryID(v any) (string, bool) {
	switch f := v.(type) {
	case *proto.QueryFrame:
		if f.DeliveryID != nil {
			return *f.DeliveryID, true
		}
		return "", false
	case proto.QueryFrame:
		if f.DeliveryID != nil {
			return *f.DeliveryID, true
		}
		return "", false
	case map[string]any:
		if id, ok := f["delivery_id"].(string); ok {
			return id, true
		}
		return "", false
	}
	return "", false
}
