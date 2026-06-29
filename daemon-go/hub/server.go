package hub

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/repowire/repowire/daemon-go/peer"
	"github.com/repowire/repowire/daemon-go/state"
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

	// Read-path deps for the HTTP route groups. Optional and nil-safe: the
	// peer-read handlers degrade gracefully (no inbound-health probe) when these
	// are unset, mirroring Python's getattr(state, ..., None) pattern. Wired via
	// WithReadDeps from main once the AskTracker / state.Store exist.
	asks  *AskTracker
	store *state.Store

	// messaging is the optional /notify + /broadcast route group, wired via
	// WithMessaging when the daemon has built a PeerDelivery. nil → those
	// endpoints are not registered (the spike daemon has no PeerDelivery yet).
	messaging *MessagingRoutes

	// lifecycle is the optional tmux-lifecycle hook route group (pane/session/
	// window/client), wired via WithLifecycle. nil → the /hooks/lifecycle/*
	// endpoints are not registered. Built in main with the tmux PaneLister.
	lifecycle *LifecycleHandler

	// ask holds the ask-lifecycle route dependencies (AskTracker + PeerDelivery
	// + the narrow registry seam), wired via WithAskLifecycle. The
	// /ask·/ack·/answer·/query·/asks/* handlers 503 while unwired.
	ask *askLifecycleDeps
}

// WithLifecycle attaches the tmux-lifecycle hook route group built over the
// supplied handler. The handler is constructed in main (it needs the tmux
// PaneLister, a host concern). Returns the hub for chaining; call before Routes.
func (h *Hub) WithLifecycle(lh *LifecycleHandler) *Hub {
	h.lifecycle = lh
	return h
}

// WithMessaging attaches the messaging (notify/broadcast) route group built over
// the supplied PeerDelivery and optional delivery-trace store. The registry is
// the LazyRepair seam; auth reuses the hub's requireAuth wrapper. Returns the
// hub for chaining; call before Routes.
func (h *Hub) WithMessaging(delivery *PeerDelivery, traces deliveryTracer) *Hub {
	h.messaging = NewMessagingRoutes(delivery, h.reg, traces)
	return h
}

// WithReadDeps injects the AskTracker and state.Store the HTTP read routes use
// to derive per-peer inbound health (pending-ask counts, last injection
// success/failure). Returns the hub for chaining. nil-safe: handlers skip the
// corresponding health fields when a dep is absent.
func (h *Hub) WithReadDeps(asks *AskTracker, store *state.Store) *Hub {
	h.asks = asks
	h.store = store
	return h
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
	h.registerPeerReadRoutes(mux)
	h.registerPeerLifecycleRoutes(mux)
	h.EventRoutes(mux)
	h.registerAskLifecycleRoutes(mux)
	if h.messaging != nil {
		h.messaging.Register(mux, h.requireAuth)
	}
	if h.lifecycle != nil {
		h.LifecycleRoutes(mux, h.lifecycle)
	}
}

// requireAuth, writeJSON, writeError, and writeJSONError are package-shared HTTP
// helpers defined in routes_ask_lifecycle.go; server.go does not redeclare them.

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
