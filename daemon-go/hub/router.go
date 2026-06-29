package hub

import (
	"context"
	"fmt"
	"time"

	"github.com/repowire/repowire/daemon-go/peer"
	"github.com/repowire/repowire/daemon-go/proto"
)

// MessageRouter turns a routing intent (send query/ask/notify to a PeerID) into
// a wire frame on the transport, and — for the blocking query path — waits on
// the QueryTracker future. Every routing function takes proto.PeerID as the
// target; passing a DisplayName is a compile error, which is the whole point.
type MessageRouter struct {
	transport *WebSocketTransport
	tracker   *QueryTracker
	reg       *peer.Registry
}

// NewMessageRouter wires the router to the transport, tracker, and registry.
func NewMessageRouter(transport *WebSocketTransport, tracker *QueryTracker, reg *peer.Registry) *MessageRouter {
	return &MessageRouter{transport: transport, tracker: tracker, reg: reg}
}

// SendQuery routes a blocking query to a peer (the legacy /query RPC shape) and
// waits up to timeout for the response. Registration happens before the send so
// the future exists before any response could land. A disconnect resolves the
// future with ErrPeerDisconnected via CancelQueriesToPeer.
func (m *MessageRouter) SendQuery(ctx context.Context, from proto.DisplayName, to proto.PeerID, toName proto.DisplayName, text string, timeout time.Duration) (string, error) {
	corrID := m.tracker.RegisterQuery(from, to, toName, text)
	future := m.tracker.Future(corrID)

	frame := proto.QueryFrame{
		Type:          proto.FrameQuery,
		CorrelationID: corrID,
		FromPeer:      from,
		Text:          text,
	}
	if err := m.transport.Send(ctx, to, frame); err != nil {
		// Send failed up front; reap the pending query so it doesn't dangle.
		m.tracker.ResolveQueryError(corrID, err)
		return "", fmt.Errorf("send query to %s: %w", to, err)
	}

	if timeout <= 0 {
		select {
		case res := <-future:
			return res.Text, res.Err
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	select {
	case res := <-future:
		return res.Text, res.Err
	case <-time.After(timeout):
		m.tracker.ResolveQueryError(corrID, fmt.Errorf("query to %s timed out", toName))
		return "", fmt.Errorf("query to %s timed out after %s", toName, timeout)
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// SendNotification routes a fire-and-forget notify frame to a peer.
// TODO(spike): full notify path (queued delivery replay, delivery receipts,
// intended-recipient mismatch handling) is out of spike depth — this stub only
// puts the frame on the wire.
func (m *MessageRouter) SendNotification(ctx context.Context, from proto.DisplayName, to proto.PeerID, text string) error {
	frame := map[string]any{
		"type":      string(proto.FrameNotify),
		"from_peer": string(from),
		"text":      text,
	}
	return m.transport.Send(ctx, to, frame)
}

// SendAsk routes a non-blocking ask frame to a peer.
// TODO(spike): full ask path (AskTracker lifecycle, Stop-hook reminder
// resurfacing, delivery-trace truthfulness) is out of spike depth — this stub
// only puts the frame on the wire.
func (m *MessageRouter) SendAsk(ctx context.Context, from proto.DisplayName, to proto.PeerID, correlationID, text string) error {
	frame := map[string]any{
		"type":           string(proto.FrameAsk),
		"correlation_id": correlationID,
		"from_peer":      string(from),
		"text":           text,
	}
	return m.transport.Send(ctx, to, frame)
}
