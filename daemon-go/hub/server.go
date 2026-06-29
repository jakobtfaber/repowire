package hub

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/repowire/repowire/daemon-go/peer"
)

// schemaVersion is reported by /health; matches the SQLite state schema version.
const schemaVersion = 12

// Hub is the network hub: it owns the transport, query tracker, message router,
// and the registry it routes against. Everything routing-sensitive flows through
// proto.PeerID. The hub wires reg.OnOffline to the tracker so a terminal/
// transport offline cascades query cancellation.
type Hub struct {
	reg       *peer.Registry
	transport *WebSocketTransport
	tracker   *QueryTracker
	router    *MessageRouter
	authToken string
}

// NewHub constructs the hub over an already-built registry, minting a fresh
// transport. The transport, tracker, and router are created here; OnOffline is
// wired so the registry can cascade query cancellation without learning about
// the tracker's shape. Use newHubWithTransport when the registry was built with
// the same transport as its liveness seam (the real wiring order in main).
func NewHub(reg *peer.Registry, authToken string) *Hub {
	return NewHubWithTransport(reg, NewWebSocketTransport(), authToken)
}

// NewHubWithTransport wraps a registry around a pre-built transport so callers
// can hand the SAME transport to peer.NewRegistry first (chicken-and-egg: the
// registry needs a peer.Transport at construction, and the hub needs that
// registry — building the transport up front breaks the cycle). This is the
// real wiring order in main.
func NewHubWithTransport(reg *peer.Registry, transport *WebSocketTransport, authToken string) *Hub {
	tracker := NewQueryTracker()
	h := &Hub{
		reg:       reg,
		transport: transport,
		tracker:   tracker,
		router:    NewMessageRouter(transport, tracker, reg),
		authToken: authToken,
	}
	reg.OnOffline = tracker.CancelQueriesToPeer
	return h
}

// Transport exposes the live transport so the registry can be constructed with
// it (peer.Transport) before the hub wraps it. Used by main wiring.
func (h *Hub) Transport() *WebSocketTransport { return h.transport }

// Router exposes the message router for HTTP routes built outside this package.
func (h *Hub) Router() *MessageRouter { return h.router }

// Routes registers the hub's HTTP handlers on the mux.
func (h *Hub) Routes(mux *http.ServeMux) {
	mux.HandleFunc("/ws", h.HandleWS)
	mux.HandleFunc("/health", h.health)
}

// health returns liveness plus the live peer count and schema version. Like /ws,
// it opportunistically kicks lazy_repair in a goroutine — maintenance piggy-
// backs on real requests, never a timer.
func (h *Hub) health(w http.ResponseWriter, r *http.Request) {
	go h.reg.LazyRepair(context.Background())
	peers := len(h.transport.GetAllSessions())
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":         "ok",
		"peers":          peers,
		"schema_version": schemaVersion,
	})
}
