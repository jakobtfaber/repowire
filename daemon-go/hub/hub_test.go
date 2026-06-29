package hub

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/repowire/repowire/daemon-go/peer"
	"github.com/repowire/repowire/daemon-go/proto"
)

// --- minimal fakes for a real Registry ---

type memStore struct{}

func (memStore) LoadMappings(context.Context) ([]*proto.SessionMapping, error) { return nil, nil }
func (memStore) UpsertMapping(context.Context, *proto.SessionMapping) error    { return nil }
func (memStore) DeleteMapping(context.Context, proto.PeerID) error             { return nil }
func (memStore) LoadRetired(context.Context, time.Time) (map[proto.PeerID]time.Time, error) {
	return map[proto.PeerID]time.Time{}, nil
}
func (memStore) Retire(context.Context, proto.PeerID, time.Time) error { return nil }
func (memStore) Unretire(context.Context, proto.PeerID) error          { return nil }
func (memStore) AppendEvent(context.Context, peer.Event) error         { return nil }

type deadLive struct{}

func (deadLive) PIDAlive(int) bool { return false }

func newTestHub(t *testing.T) *Hub {
	t.Helper()
	// Build the transport first so the registry's liveness seam is the same
	// transport the hub serves on (ghost eviction sees the live sockets), then
	// wrap that registry+transport in the hub.
	transport := NewWebSocketTransport()
	reg, err := peer.NewRegistry(context.Background(), memStore{}, deadLive{}, transport)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	return NewHubWithTransport(reg, transport, "")
}

// TestRouterRoutesByPeerID is the core smoke test: two peers are connected, a
// query is sent to ONE of them by PeerID, and only that peer's socket receives
// the frame. Routing must be keyed on PeerID, not DisplayName.
func TestRouterRoutesByPeerID(t *testing.T) {
	h := newTestHub(t)
	srv := httptest.NewServer(http.HandlerFunc(h.HandleWS))
	defer srv.Close()
	wsURL := "ws" + srv.URL[len("http"):]

	connectPeer := func(name string) (*websocket.Conn, proto.PeerID) {
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
		if connected.Type != proto.FrameConnected {
			t.Fatalf("expected connected frame, got %s", connected.Type)
		}
		return c, connected.SessionID
	}

	cA, idA := connectPeer("alpha")
	defer cA.CloseNow()
	cB, idB := connectPeer("beta")
	defer cB.CloseNow()

	if idA == idB {
		t.Fatalf("distinct peers must get distinct peer_ids: %s", idA)
	}

	// Wait for both transports to register.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if h.transport.IsConnected(idA) && h.transport.IsConnected(idB) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !h.transport.IsConnected(idA) || !h.transport.IsConnected(idB) {
		t.Fatalf("both peers should be connected")
	}

	// Send a query to peer B only; peer B's socket must receive it, peer A must not.
	go func() {
		_, _ = h.router.SendQuery(context.Background(), "tester", idB, "beta", "ping?", 2*time.Second)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var q proto.QueryFrame
	if err := wsjson.Read(ctx, cB, &q); err != nil {
		t.Fatalf("peer B should receive the query: %v", err)
	}
	if q.Type != proto.FrameQuery || q.Text != "ping?" {
		t.Fatalf("unexpected query frame on B: %+v", q)
	}

	// Peer A must NOT have received anything: a short read should time out.
	shortCtx, shortCancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer shortCancel()
	var stray proto.QueryFrame
	if err := wsjson.Read(shortCtx, cA, &stray); err == nil {
		t.Fatalf("peer A must NOT receive a query routed to B, got %+v", stray)
	}
}

// TestDisconnectIdentityChecked verifies the disconnect race guard: a stale
// handler holding an old socket must not evict a newer connection stored under
// the same peer_id.
func TestDisconnectIdentityChecked(t *testing.T) {
	tr := NewWebSocketTransport()
	id := proto.PeerID("repow-default-aaaa1111")

	// Two distinct sentinel *websocket.Conn values; we never read/write them, only
	// compare identity, so unconnected zero conns suffice via net.Pipe-backed accept.
	oldWS, closeOld := dummyConn(t)
	defer closeOld()
	newWS, closeNew := dummyConn(t)
	defer closeNew()

	tr.Connect(context.Background(), &ConnectionInfo{SessionID: id, WS: oldWS})
	tr.Connect(context.Background(), &ConnectionInfo{SessionID: id, WS: newWS})

	// The stale handler (oldWS) tears down: must NOT evict newWS.
	if tr.Disconnect(context.Background(), id, oldWS) {
		t.Fatalf("stale-socket disconnect must return false")
	}
	if !tr.IsConnected(id) {
		t.Fatalf("newer connection must survive a stale disconnect")
	}
	// The owning handler (newWS) tears down: must evict.
	if !tr.Disconnect(context.Background(), id, newWS) {
		t.Fatalf("owning-socket disconnect must return true")
	}
	if tr.IsConnected(id) {
		t.Fatalf("peer should be gone after owning disconnect")
	}
}

// dummyConn establishes a real *websocket.Conn (the server-accepted side) so the
// disconnect guard test has genuine, distinct connection pointers to compare.
func dummyConn(t *testing.T) (*websocket.Conn, func()) {
	t.Helper()
	accepted := make(chan *websocket.Conn, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept: %v", err)
			return
		}
		accepted <- c
		// Hold the handler open until the test tears the connection down.
		<-r.Context().Done()
	}))
	wsURL := "ws" + srv.URL[len("http"):]
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		srv.Close()
		t.Fatalf("dial: %v", err)
	}
	server := <-accepted
	return server, func() {
		client.CloseNow()
		server.CloseNow()
		srv.Close()
	}
}
