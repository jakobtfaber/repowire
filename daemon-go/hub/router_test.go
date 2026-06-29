package hub

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/repowire/repowire/daemon-go/proto"
)

// fakeTransport is a hand-driven Transport fake so router tests can control the
// delivery_ack timing/shape without standing up a real socket. It records the
// last frame and the target it was sent to, so tests assert routing by PeerID.
type fakeTransport struct {
	sessions  []proto.PeerID
	paneIDs   map[proto.PeerID]string
	connected map[proto.PeerID]bool

	// ack governs SendAndWaitDeliveryAck: if ackDelay > timeout the call returns
	// (nil,nil) (the best-effort timeout path); otherwise it returns ackFrame.
	ackFrame map[string]any
	ackDelay time.Duration
	ackErr   error

	sendErr error

	// captured state
	lastTarget  proto.PeerID
	lastFrame   any
	sentTargets []proto.PeerID
}

func (f *fakeTransport) Send(ctx context.Context, id proto.PeerID, v any) error {
	f.lastTarget = id
	f.lastFrame = v
	f.sentTargets = append(f.sentTargets, id)
	return f.sendErr
}

func (f *fakeTransport) SendAndWaitDeliveryAck(ctx context.Context, id proto.PeerID, v any, timeout time.Duration) (map[string]any, error) {
	f.lastTarget = id
	f.lastFrame = v
	if f.ackErr != nil {
		return nil, f.ackErr
	}
	if f.ackDelay >= timeout {
		// Best-effort timeout: older hooks never ack.
		return nil, nil
	}
	return f.ackFrame, nil
}

func (f *fakeTransport) IsConnected(id proto.PeerID) bool { return f.connected[id] }

func (f *fakeTransport) GetAllSessions() []proto.PeerID { return f.sessions }

func (f *fakeTransport) ConnectionPaneID(id proto.PeerID) (string, bool) {
	p, ok := f.paneIDs[id]
	return p, ok
}

func (f *fakeTransport) Ping(ctx context.Context, id proto.PeerID, timeout time.Duration) (map[string]any, error) {
	return nil, nil
}

func (f *fakeTransport) ACPRoute(target *proto.Peer) (*ACPRouteDecision, bool) { return nil, false }

func newRouterWithFake(f *fakeTransport) *MessageRouter {
	// reg is unused by the send paths under test; nil is fine for unit coverage.
	return NewMessageRouter(f, NewQueryTracker(), nil)
}

// TestSendAskReturnsHookReceiptWithinAckWindow exercises the happy delivery-ack
// path: the hook acks "injected" before the 750ms window, so SendAsk returns the
// receipt and no error.
func TestSendAskReturnsHookReceiptWithinAckWindow(t *testing.T) {
	f := &fakeTransport{
		ackFrame: map[string]any{"status": "injected", "delivery_id": "ask-delivery-x"},
		ackDelay: 0, // resolves immediately, well within deliveryAckTimeout
	}
	m := newRouterWithFake(f)

	hook, err := m.SendAsk(context.Background(), "alpha", "repow-default-bbbb2222", "beta", "beta",
		"cid-1", "need a hand", nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hook["status"] != "injected" {
		t.Fatalf("expected injected receipt, got %+v", hook)
	}
	if f.lastTarget != "repow-default-bbbb2222" {
		t.Fatalf("ask must route by PeerID, got %s", f.lastTarget)
	}
}

// TestSendAskNoAckIsNotAnError is the 750ms best-effort timeout path: a legacy
// hook that never acks must yield (nil, nil), not an error. The fake's ackDelay
// >= the router's deliveryAckTimeout makes SendAndWaitDeliveryAck return the
// timeout sentinel.
func TestSendAskNoAckIsNotAnError(t *testing.T) {
	f := &fakeTransport{
		ackFrame: map[string]any{"status": "injected"},
		ackDelay: deliveryAckTimeout, // == timeout → timeout branch → (nil,nil)
	}
	m := newRouterWithFake(f)

	hook, err := m.SendAsk(context.Background(), "alpha", "repow-default-cccc3333", "gamma", "gamma",
		"cid-2", "ping", nil, nil, nil)
	if err != nil {
		t.Fatalf("a missing ack must not be an error, got %v", err)
	}
	if hook != nil {
		t.Fatalf("expected nil hook receipt on no-ack, got %+v", hook)
	}
}

// TestSendAskLoudFailureOnReject is the FAIL-LOUD contract: a delivery_ack with
// status rejected/failed must surface as a *DeliveryInjectionError carrying the
// hook receipt — never a silent success.
func TestSendAskLoudFailureOnReject(t *testing.T) {
	for _, status := range []string{"rejected", "failed"} {
		t.Run(status, func(t *testing.T) {
			f := &fakeTransport{
				ackFrame: map[string]any{"status": status, "detail": "pane not safe"},
				ackDelay: 0,
			}
			m := newRouterWithFake(f)

			hook, err := m.SendAsk(context.Background(), "alpha", "repow-default-dddd4444", "delta", "delta",
				"cid-3", "do the thing", nil, nil, nil)
			if err == nil {
				t.Fatalf("expected a loud DeliveryInjectionError for status=%s, got hook=%+v", status, hook)
			}
			di, ok := AsDeliveryInjection(err)
			if !ok {
				t.Fatalf("expected *DeliveryInjectionError, got %T: %v", err, err)
			}
			if di.Status != status {
				t.Fatalf("expected status %s, got %s", status, di.Status)
			}
			if di.Detail != "pane not safe" {
				t.Fatalf("expected detail propagated, got %q", di.Detail)
			}
			if di.HookDelivery["status"] != status {
				t.Fatalf("hook receipt must be carried on the error, got %+v", di.HookDelivery)
			}
			if hook != nil {
				t.Fatalf("loud failure must return nil hook, got %+v", hook)
			}
		})
	}
}

// TestSendAskRejectDetailFallsBackToStatus checks the detail fallback: when the
// hook supplies no detail, the error detail is the status string.
func TestSendAskRejectDetailFallsBackToStatus(t *testing.T) {
	f := &fakeTransport{ackFrame: map[string]any{"status": "rejected"}, ackDelay: 0}
	m := newRouterWithFake(f)
	_, err := m.SendAsk(context.Background(), "a", "repow-default-eeee5555", "e", "e", "cid", "t", nil, nil, nil)
	di, ok := AsDeliveryInjection(err)
	if !ok {
		t.Fatalf("expected DeliveryInjectionError, got %v", err)
	}
	if di.Detail != "rejected" {
		t.Fatalf("detail should fall back to status, got %q", di.Detail)
	}
}

// TestSendAskMintsDeliveryIDAndCloseHint verifies the ask frame shape: a minted
// ask-delivery-* id, the ack/answer close hint adapting to question presence,
// and reply_to threading.
func TestSendAskMintsDeliveryIDAndCloseHint(t *testing.T) {
	f := &fakeTransport{ackFrame: nil, ackDelay: 0}
	m := newRouterWithFake(f)

	replyTo := "parent-cid"
	_, err := m.SendAsk(context.Background(), "alpha", "repow-default-ffff6666", "zeta", "zeta",
		"cid-9", "review please", &replyTo, map[string]any{"prompt": "ok?"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	frame, ok := f.lastFrame.(map[string]any)
	if !ok {
		t.Fatalf("expected map frame, got %T", f.lastFrame)
	}
	if did, _ := frame["delivery_id"].(string); len(did) == 0 || did[:13] != "ask-delivery-" {
		t.Fatalf("expected minted ask-delivery-* id, got %q", frame["delivery_id"])
	}
	if frame["reply_to"] != "parent-cid" {
		t.Fatalf("reply_to must thread through, got %v", frame["reply_to"])
	}
	if frame["question"] == nil {
		t.Fatalf("question envelope must be carried")
	}
	txt, _ := frame["text"].(string)
	if want := `↳ answer("cid-9"`; len(txt) == 0 || !contains(txt, want) {
		t.Fatalf("question ask must carry answer() close hint, got %q", txt)
	}
}

// TestSendNotificationMintsDeliveryID covers the notify wire shape: a minted
// notif-delivery-* id when none supplied, and a returned receipt when the hook
// acks within the window. A missing ack is not an error.
func TestSendNotificationMintsDeliveryID(t *testing.T) {
	f := &fakeTransport{ackFrame: map[string]any{"status": "injected"}, ackDelay: 0}
	m := newRouterWithFake(f)

	hook, err := m.SendNotification(context.Background(), "alpha", "repow-default-aaaa1111", "beta", "beta",
		"fyi", nil, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hook["status"] != "injected" {
		t.Fatalf("expected receipt, got %+v", hook)
	}
	frame := f.lastFrame.(map[string]any)
	if did, _ := frame["delivery_id"].(string); len(did) < 15 || did[:15] != "notif-delivery-" {
		t.Fatalf("expected minted notif-delivery-* id, got %q", frame["delivery_id"])
	}
	if f.lastTarget != "repow-default-aaaa1111" {
		t.Fatalf("notify must route by PeerID, got %s", f.lastTarget)
	}
}

// TestSendNotificationHonorsSuppliedDeliveryID verifies an explicit delivery_id
// is preserved (the queued-delivery replay path supplies a stable id).
func TestSendNotificationHonorsSuppliedDeliveryID(t *testing.T) {
	f := &fakeTransport{ackFrame: nil, ackDelay: 0}
	m := newRouterWithFake(f)
	_, _ = m.SendNotification(context.Background(), "a", "repow-default-aaaa1111", "b", "b", "hi", nil, "fixed-id")
	frame := f.lastFrame.(map[string]any)
	if frame["delivery_id"] != "fixed-id" {
		t.Fatalf("supplied delivery_id must be preserved, got %v", frame["delivery_id"])
	}
}

// TestBroadcastExcludesAndCollectsFailures: fan-out hits every connected peer
// minus the excludes; one recipient failure is reported, not fatal to the rest.
func TestBroadcastExcludesAndCollectsFailures(t *testing.T) {
	a := proto.PeerID("repow-default-aaaa1111")
	b := proto.PeerID("repow-default-bbbb2222")
	c := proto.PeerID("repow-default-cccc3333")
	f := &fakeTransport{sessions: []proto.PeerID{a, b, c}}
	m := newRouterWithFake(f)

	// Exclude the sender (a). b and c receive. (No sendErr → both succeed.)
	sent, failed := m.Broadcast(context.Background(), "alpha", "hello mesh",
		map[proto.PeerID]struct{}{a: {}})
	if len(failed) != 0 {
		t.Fatalf("expected no failures, got %+v", failed)
	}
	if len(sent) != 2 {
		t.Fatalf("expected 2 recipients (b,c), got %v", sent)
	}
	for _, id := range sent {
		if id == a {
			t.Fatalf("excluded sender must not receive its own broadcast")
		}
	}
}

// TestBroadcastOneFailureDoesNotAbortFanout: a transport error to one peer is
// recorded as a per-recipient failure and the rest still go out.
func TestBroadcastOneFailureDoesNotAbortFanout(t *testing.T) {
	a := proto.PeerID("repow-default-aaaa1111")
	f := &fakeTransport{sessions: []proto.PeerID{a}, sendErr: errors.New("boom")}
	m := newRouterWithFake(f)
	sent, failed := m.Broadcast(context.Background(), "alpha", "x", nil)
	if len(sent) != 0 {
		t.Fatalf("a failed send must not be counted as sent")
	}
	if len(failed) != 1 || failed[0].PeerID != a {
		t.Fatalf("expected one recorded failure for %s, got %+v", a, failed)
	}
}

// TestSendAskRealSocketAckWithin750ms exercises the GENUINE delivery-ack window
// end-to-end: the router sends an ask over a real WebSocketTransport, the peer
// reads the frame and replies with a delivery_ack inside the 750ms window, and
// the hub's dispatch resolves it so SendAsk returns the receipt. This proves the
// real SendAndWaitDeliveryAck path, not just the fake's branching.
func TestSendAskRealSocketAckWithin750ms(t *testing.T) {
	h := newTestHub(t)
	srv := httptest.NewServer(http.HandlerFunc(h.HandleWS))
	defer srv.Close()
	wsURL := "ws" + srv.URL[len("http"):]

	c, peerID := dialAndConnect(t, wsURL, "beta")
	defer c.CloseNow()
	waitConnected(t, h, peerID)

	// Peer side: read the ask frame, ack it as injected well within 750ms.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, raw, err := c.Read(ctx)
		if err != nil {
			return
		}
		var frame map[string]any
		_ = json.Unmarshal(raw, &frame)
		_ = wsjson.Write(ctx, c, map[string]any{
			"type":        string(proto.FrameDeliveryAck),
			"delivery_id": frame["delivery_id"],
			"status":      "injected",
		})
	}()

	start := time.Now()
	hook, err := h.router.SendAsk(context.Background(), "alpha", peerID, "beta", "beta",
		"cid-real", "real ask", nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hook == nil || hook["status"] != "injected" {
		t.Fatalf("expected injected receipt over real socket, got %+v", hook)
	}
	if elapsed := time.Since(start); elapsed >= deliveryAckTimeout {
		t.Fatalf("ack should have resolved before the 750ms window, took %s", elapsed)
	}
}

// TestSendNotificationRealSocketNoAckTimesOut covers the genuine 750ms timeout:
// the peer reads the notify frame but never acks (legacy hook), so the real
// SendAndWaitDeliveryAck waits the full window and returns (nil, nil) — a
// missing ack is best-effort, never an error.
func TestSendNotificationRealSocketNoAckTimesOut(t *testing.T) {
	h := newTestHub(t)
	srv := httptest.NewServer(http.HandlerFunc(h.HandleWS))
	defer srv.Close()
	wsURL := "ws" + srv.URL[len("http"):]

	c, peerID := dialAndConnect(t, wsURL, "gamma")
	defer c.CloseNow()
	waitConnected(t, h, peerID)

	// Peer reads but deliberately never acks.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, _, _ = c.Read(ctx)
	}()

	start := time.Now()
	hook, err := h.router.SendNotification(context.Background(), "alpha", peerID, "gamma", "gamma",
		"fyi no ack", nil, "")
	if err != nil {
		t.Fatalf("a missing ack must not error, got %v", err)
	}
	if hook != nil {
		t.Fatalf("expected nil receipt on no-ack, got %+v", hook)
	}
	if elapsed := time.Since(start); elapsed < deliveryAckTimeout {
		t.Fatalf("no-ack notify should wait the full 750ms window, only waited %s", elapsed)
	}
}

// dialAndConnect dials the hub, sends a connect frame, and returns the live
// client conn plus the assigned peer_id.
func dialAndConnect(t *testing.T, wsURL, name string) (*websocket.Conn, proto.PeerID) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if err := wsjson.Write(ctx, c, proto.ConnectFrame{
		Type:        proto.FrameConnect,
		DisplayName: proto.DisplayName(name),
		Circle:      "default",
		Backend:     proto.AgentClaudeCode,
		Role:        proto.RoleAgent,
	}); err != nil {
		t.Fatalf("write connect: %v", err)
	}
	var connected proto.ConnectedFrame
	if err := wsjson.Read(ctx, c, &connected); err != nil {
		t.Fatalf("read connected: %v", err)
	}
	return c, connected.SessionID
}

// waitConnected blocks until the transport registers the peer's socket.
func waitConnected(t *testing.T, h *Hub, id proto.PeerID) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if h.transport.IsConnected(id) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("peer %s never connected", id)
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
