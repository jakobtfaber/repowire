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

	// schedules is the optional /schedules route group, wired via WithSchedules
	// once the daemon has built the schedules store + scheduler. nil → those
	// endpoints are not registered.
	schedules *ScheduleRoutes

	// session holds the session-wiring route dependencies (registry + query
	// tracker + queued-delivery store), wired via WithSessionRoutes. The
	// /session/update·/response·/deliveries/pending handlers 503 while unwired.
	session *sessionDeps

	// reviews is the JSON-backed review-queue store behind the /reviews routes,
	// wired via WithReviews. nil → a default store at ~/.repowire/review_queue.json
	// is created lazily at Routes() time so the endpoints are always available.
	reviews *ReviewQueueStore

	// relay is the optional relay config behind the /shares proxy routes, wired
	// via WithShares. nil (or disabled) → POST/DELETE 503, GET returns []
	// (matches Python when relay is not configured).
	relay *RelayConfig

	// work holds the tracked-work / durable-job route dependencies (work store,
	// JobRunner, SessionControl, assigned-peer resolver), wired via WithWork /
	// WithWorkRegistry. The /work·/jobs handlers 503 while unwired.
	work *workRoutes

	// spawn holds the spawn-kill-restart route dependencies (SpawnService + the
	// narrow spawnRegistry seam + AskTracker quiesce barrier), wired via WithSpawn.
	// nil → the /spawn·/kill-peer·/peers/{name}/{restart,switch-backend,rehook}
	// handlers 503. Built in main with the real TmuxController + PaneOwnership.
	spawn *spawnDeps
}

// WithReviews wires an explicit review-queue store onto the hub (e.g. a test
// store under a temp dir). When unset, Routes() lazily creates the default
// JSON-backed store. Returns the hub for chaining; call before Routes.
func (h *Hub) WithReviews(store *ReviewQueueStore) *Hub {
	h.reviews = store
	return h
}

// WithSchedules attaches the /schedules route group built over the schedules
// store and the scheduler wake. The scheduler's firing loop is started/stopped
// by main (it owns the goroutine lifecycle); this only wires the routes.
// Returns the hub for chaining; call before Routes.
func (h *Hub) WithSchedules(store scheduleStore, scheduler scheduleWaker) *Hub {
	h.schedules = NewScheduleRoutes(store, scheduler)
	return h
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
	h.registerOrchestratorRoutes(mux)
	h.EventRoutes(mux)
	h.EventsStreamRoutes(mux)
	h.registerAskLifecycleRoutes(mux)
	if h.session != nil {
		h.registerSessionRoutes(mux)
	}
	if h.messaging != nil {
		h.messaging.Register(mux, h.requireAuth)
	}
	if h.lifecycle != nil {
		h.LifecycleRoutes(mux, h.lifecycle)
	}
	if h.schedules != nil {
		h.schedules.Register(mux, h.requireAuth)
	}
	if h.work != nil {
		h.registerWorkRoutes(mux)
	}
	if h.spawn != nil {
		h.registerSpawnRoutes(mux)
	}
	// reviews/shares/attachments are independent leaf endpoints, always
	// registered. Reviews lazily defaults its store; shares/attachments degrade
	// gracefully when their (relay) dependency is unset.
	if h.reviews == nil {
		h.reviews = NewReviewQueueStore(DefaultReviewQueuePath())
	}
	h.registerReviewRoutes(mux)
	h.registerShareRoutes(mux)
	h.registerAttachmentRoutes(mux)
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
