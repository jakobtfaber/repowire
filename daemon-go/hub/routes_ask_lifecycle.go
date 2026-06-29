package hub

// Ask-lifecycle HTTP route group: the non-blocking ask/ack model plus the
// blocking /query compat shim. Port of repowire/daemon/routes/asks.py +
// messages.py (the /query handler) and ask_service.py (the ack/answer reply
// flow). Endpoints:
//
//	POST /ask                          register + deliver an ask
//	POST /ack                          close an ask (bare or with reply body)
//	POST /answer                       answer a structured-question ask
//	POST /query                        legacy blocking RPC (ask-based shim)
//	GET  /asks/pending                 the Stop-hook reminder source
//	POST /asks/{cid}/picked_up         deprecated no-op (transport compat)
//	POST /asks/{cid}/mark_reminded     deprecated no-op (hook compat)
//	POST /asks/{cid}/wait              bounded wait for resolution (wait_on_ack)
//
// Identity discipline: routing-sensitive lookups resolve to proto.PeerID via the
// registry / AskTracker; reply routing in /ack and /answer uses the STORED ask
// recipient (ask.ToPeerID), never the request's compat from_peer. Fail loud over
// silent-degrade: a DeliveryInjectionError is a 503 with the ask left for the
// route to close as send_failed (the peer is NOT marked unreachable — the socket
// is alive); an undeliverable ack reply is a 503 with the ask left OPEN for retry.

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/repowire/repowire/daemon-go/proto"
)

// askRoutesRegistry is the narrow registry seam the ask-lifecycle routes need.
// It mirrors the PeerRegistry methods the Python asks.py / messages.py handlers
// call (get_peer_by_pane, get_peer, get_peer-by-name, add_event).
//
// ponytail: a narrow seam because the REGISTRY port (which adds GetPeerByPane /
// GetPeerByName / AddEvent to *peer.Registry) is still in flight — the same
// reason hub.accessRegistry (delivery.go) is narrow. *peer.Registry satisfies
// this once those land; the handler bodies don't change. Kept narrow also keeps
// the route handler test hermetic (no SQLite, no live transport).
type askRoutesRegistry interface {
	// GetPeerByPane resolves a tmux-pane-keyed transport (Claude Code / Codex /
	// Gemini Stop hooks) to its peer. (nil,false) when no peer owns the pane.
	GetPeerByPane(pane string) (*proto.Peer, bool)
	// GetPeer resolves by peer_id (the canonical key). (nil,false) when unknown.
	GetPeer(id proto.PeerID) (*proto.Peer, bool)
	// GetPeerByName resolves a display_name within an optional circle scope. Used
	// by /query's pre-check and /ask's sender/target resolution. err is the
	// ambiguous-name (ValueError) rejection.
	GetPeerByName(name string, circle *string) (*proto.Peer, error)
	// AddEvent records a journal event (query/response audit). Best-effort.
	AddEvent(ctx context.Context, typ string, payload map[string]any) (eventID string)
}

// askLifecycleDeps bundles the services the ask-lifecycle routes compose. Wired
// onto the Hub via WithAskLifecycle; nil-safe (handlers 503 when unwired).
type askLifecycleDeps struct {
	asks     *AskTracker
	delivery *PeerDelivery
	reg      askRoutesRegistry
}

// WithAskLifecycle wires the ask-lifecycle route dependencies onto the hub. The
// concrete *peer.Registry satisfies askRoutesRegistry once the registry port
// lands; until then a test/fake registry is passed. Returns the receiver.
func (h *Hub) WithAskLifecycle(asks *AskTracker, delivery *PeerDelivery, reg askRoutesRegistry) *Hub {
	h.ask = &askLifecycleDeps{asks: asks, delivery: delivery, reg: reg}
	return h
}

// askWaitMax mirrors ASK_WAIT_MAX_SECONDS: the hard cap on how long /asks/{cid}/wait
// holds a connection open. The client timeout must sit above this + margin.
const (
	askWaitMaxSeconds     = 50 * time.Second
	askWaitDefaultSeconds = 45 * time.Second
	defaultQueryTimeout   = 60 * time.Second
)

// registerAskLifecycleRoutes attaches the ask-lifecycle handlers to the mux. The
// {correlation_id} path endpoints are served off the "/asks/" subtree prefix
// handler so the stdlib ServeMux (pre-1.22 pattern semantics in this module)
// can dispatch them by suffix without a router dependency.
func (h *Hub) registerAskLifecycleRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/ask", h.requireAuth(h.handleAsk))
	mux.HandleFunc("/ack", h.requireAuth(h.handleAck))
	mux.HandleFunc("/answer", h.requireAuth(h.handleAnswer))
	mux.HandleFunc("/query", h.requireAuth(h.handleQuery))
	mux.HandleFunc("/asks/pending", h.requireAuth(h.handlePendingAsks))
	// /asks/{correlation_id}/{picked_up|mark_reminded|wait}
	mux.HandleFunc("/asks/", h.requireAuth(h.handleAskSubpath))
}

// ----------------------------------------------------------------------------
// HTTP plumbing (package-shared). This route group is the canonical home of the
// hub's HTTP helpers — the requireAuth bearer-token wrapper, writeJSON,
// writeError, writeJSONError, and decodeJSON. Other route groups call
// h.requireAuth(handler) and reuse these writers without redeclaring them.
// ----------------------------------------------------------------------------

// requireAuth gates an HTTP handler behind the daemon bearer token. An empty
// configured token (h.authToken == "") disables auth (dev/local). Otherwise an
// "Authorization: Bearer <token>" header must match in constant time. Mirrors
// daemon/auth.py require_auth (401 on missing/invalid).
func (h *Hub) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if h.authToken == "" {
			next(w, r)
			return
		}
		const prefix = "Bearer "
		got := r.Header.Get("Authorization")
		if !strings.HasPrefix(got, prefix) {
			writeError(w, http.StatusUnauthorized, "Missing authorization header")
			return
		}
		token := strings.TrimPrefix(got, prefix)
		if subtle.ConstantTimeCompare([]byte(token), []byte(h.authToken)) != 1 {
			writeError(w, http.StatusUnauthorized, "Invalid auth token")
			return
		}
		next(w, r)
	}
}

// writeJSON encodes v as the response body with the given status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError writes a FastAPI-shaped error body ({"detail": <string>}) with the
// given status, matching the Python daemon's HTTPException wire shape.
func writeError(w http.ResponseWriter, status int, detail string) {
	writeJSON(w, status, map[string]any{"detail": detail})
}

// writeJSONError emits the {"detail": ...} envelope for a structured (map)
// detail. A plain string detail is routed through writeError.
func writeJSONError(w http.ResponseWriter, status int, detail any) {
	if s, ok := detail.(string); ok {
		writeError(w, status, s)
		return
	}
	writeJSON(w, status, map[string]any{"detail": detail})
}

// decodeJSON reads the request body into dst, 400 on malformed JSON.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

// askReady reports whether the ask-lifecycle deps are wired; 503 otherwise.
func (h *Hub) askReady(w http.ResponseWriter) bool {
	if h.ask == nil || h.ask.asks == nil || h.ask.delivery == nil || h.ask.reg == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "ask lifecycle not configured")
		return false
	}
	return true
}

// ----------------------------------------------------------------------------
// POST /ask
// ----------------------------------------------------------------------------

// AskRequest is the /ask body. Wire shape matches asks.py AskRequest.
type AskRequest struct {
	FromPeer     string           `json:"from_peer"`
	ToPeer       string           `json:"to_peer"`
	Text         string           `json:"text"`
	Attachments  []map[string]any `json:"attachments,omitempty"`
	ReplyTo      *string          `json:"reply_to,omitempty"`
	BypassCircle bool             `json:"bypass_circle,omitempty"`
	Circle       *string          `json:"circle,omitempty"`
	Question     map[string]any   `json:"question,omitempty"`
}

// AskResponse mirrors asks.py AskResponse.
type AskResponse struct {
	CorrelationID string  `json:"correlation_id"`
	Error         *string `json:"error,omitempty"`
}

// handleAsk opens a non-blocking ask: resolve+authorize via CheckAccess (inside
// DeliverAsk), register in the AskTracker (minting ask-<hex8> or reusing the
// caller-supplied cid), then deliver. On ErrQuiesced → 409; on a
// DeliveryInjectionError → close send_failed + 503 {injection_failed}; on a
// genuine TransportError → close send_failed + 503 (the peer is marked offline
// inside DeliverAsk). reply_to closes the referenced prior ask on success.
func (h *Hub) handleAsk(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	if !h.askReady(w) {
		return
	}
	var req AskRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	ctx := r.Context()

	// Resolve the target FIRST so the AskTracker entry is keyed on the canonical
	// peer_id (display names collide; PendingForPeer / reply routing are
	// peer_id-keyed). Mirrors AskService.open_ask resolving the peer before
	// register. An ambiguous name is a 409; an unknown one a 404.
	target, terr := h.ask.reg.GetPeerByName(req.ToPeer, req.Circle)
	if terr != nil {
		writeJSONError(w, http.StatusConflict, terr.Error())
		return
	}
	if target == nil {
		writeJSONError(w, http.StatusNotFound, "Unknown peer: "+req.ToPeer)
		return
	}
	// Best-effort sender resolution (preferring the target's circle); an
	// unresolved sender still proceeds (the from fields stay as supplied).
	fromID := proto.PeerID(req.FromPeer)
	fromName := proto.DisplayName(req.FromPeer)
	if from, ferr := h.ask.reg.GetPeerByName(req.FromPeer, &target.Circle); ferr == nil && from != nil {
		fromID = from.PeerID
		fromName = from.DisplayName
	}

	// reply_to closes a PRIOR ask; it is NOT this ask's cid. Register with an
	// empty CorrelationID so a fresh ask-<hex8> is minted.
	cid, err := h.ask.asks.Register(ctx, RegisterAskParams{
		FromPeerID:   fromID,
		FromPeerName: fromName,
		ToPeerID:     target.PeerID,
		ToPeerName:   target.DisplayName,
		Text:         req.Text,
		ReplyTo:      req.ReplyTo,
		Question:     req.Question,
	})
	if err != nil {
		if errors.Is(err, ErrQuiesced) {
			writeJSONError(w, http.StatusConflict, map[string]any{
				"error": "peer_switching",
				"hint":  fmt.Sprintf("Peer %s is mid-switch; retry shortly.", req.ToPeer),
			})
			return
		}
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	_, err = h.ask.delivery.DeliverAsk(ctx, DeliverAskParams{
		FromPeer:      string(fromID),
		ToPeer:        string(target.PeerID),
		Text:          req.Text,
		CorrelationID: cid,
		ReplyTo:       req.ReplyTo,
		BypassCircle:  true, // sender already authorized; deliver_ask bypasses re-gating (asks.py: bypass_circle=True)
		Circle:        req.Circle,
		Attachments:   req.Attachments,
		Question:      req.Question,
	})
	if err != nil {
		if di, ok := AsDeliveryInjection(err); ok {
			// Fail loud: hook reached, pane rejected. Record injection_failed and
			// 503; the socket is alive so the peer is NOT marked unreachable.
			h.ask.reg.AddEvent(ctx, "delivery_trace", map[string]any{
				"trace_id": cid, "kind": "ask", "stage": "injection_failed",
				"status": "fail", "detail": di.Detail, "hook_delivery": di.HookDelivery,
			})
			_, _ = h.ask.asks.Close(ctx, cid, "send_failed")
			writeJSONError(w, http.StatusServiceUnavailable, map[string]any{
				"error":          "injection_failed",
				"hint":           fmt.Sprintf("Ask injection failed for %s: %s", req.ToPeer, di.Error()),
				"correlation_id": cid,
			})
			return
		}
		// Unknown target / circle violation surfaced by CheckAccess, or a genuine
		// no-connection TransportError. Either way the ask cannot stand: close it.
		_, _ = h.ask.asks.Close(ctx, cid, "send_failed")
		if errors.Is(err, ErrNotConnected) {
			writeJSONError(w, http.StatusServiceUnavailable,
				fmt.Sprintf("Peer %s has no live connection: %s", req.ToPeer, err))
			return
		}
		writeJSONError(w, http.StatusNotFound, err.Error())
		return
	}

	// reply_to: close the referenced prior ask now that the new one landed.
	if req.ReplyTo != nil {
		_, _ = h.ask.asks.Close(ctx, *req.ReplyTo, "reply_to")
	}
	writeJSON(w, http.StatusOK, AskResponse{CorrelationID: cid})
}

// ----------------------------------------------------------------------------
// POST /ack
// ----------------------------------------------------------------------------

// AckRequest mirrors asks.py AckRequest. from_peer is compat-only; reply routing
// uses the stored ask recipient.
type AckRequest struct {
	CorrelationID string           `json:"correlation_id"`
	Message       *string          `json:"message,omitempty"`
	Attachments   []map[string]any `json:"attachments,omitempty"`
	FromPeer      *string          `json:"from_peer,omitempty"`
}

// handleAck closes an ask. Bare ack → Close(ack), idempotent re-ack of a closed
// ask → 200. A structured-question ask delegates to /answer. Ack-with-message
// delivers the reply to the ORIGINAL asker first (framed "[ack #cid from
// @<recipient>] <msg>") and only closes ack_with_msg on success: 410 if the ask
// is already closed (reply undeliverable), 503 if the reply can't be delivered
// (ask stays open for retry). Mirrors AskService.ack.
func (h *Hub) handleAck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	if !h.askReady(w) {
		return
	}
	var req AckRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	ctx := r.Context()

	existing, ok := h.ask.asks.Get(req.CorrelationID)
	if !ok {
		writeJSONError(w, http.StatusNotFound,
			"No open ask with correlation_id: "+req.CorrelationID)
		return
	}

	hasBody := (req.Message != nil && *req.Message != "") || len(req.Attachments) > 0

	// Structured question, still open → /answer is the canonical verb.
	if existing.Question != nil && !existing.Closed {
		outcome := "acknowledged"
		if hasBody {
			outcome = "answered"
		}
		h.answerInternal(w, r, AnswerRequest{
			CorrelationID: req.CorrelationID,
			Text:          req.Message,
			Outcome:       outcome,
			Attachments:   req.Attachments,
		})
		return
	}

	// Already closed: a reply can no longer be delivered.
	if existing.Closed {
		if hasBody {
			writeJSONError(w, http.StatusGone, fmt.Sprintf(
				"Ask %s is already closed; reply message was not delivered. "+
					"Send a new notify/ask instead.", req.CorrelationID))
			return
		}
		// Idempotent bare re-ack.
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}

	// Pull delivery (asker blocked in wait_on_ack): retain the reply on the ask
	// and let the resolved waiter deliver it, instead of injecting into a pane
	// nobody is reading.
	if existing.ReplyDelivery == "pull" && hasBody {
		h.ask.asks.CaptureReply(ctx, req.CorrelationID, derefOr(req.Message, ""), req.Attachments)
		_, _ = h.ask.asks.Close(ctx, req.CorrelationID, "ack_with_msg")
		h.emitAckEvent(ctx, existing, "ack_with_msg", true, true, len(req.Attachments) > 0)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}

	if hasBody {
		framed := fmt.Sprintf("[ack #%s from @%s] %s",
			req.CorrelationID, existing.ToPeerName, derefOr(req.Message, ""))
		// Routing uses the STORED ask endpoints, never req.FromPeer (compat-only).
		res, err := h.ask.delivery.Notify(ctx, NotifyParams{
			FromPeer:     string(existing.ToPeerID),
			ToPeer:       string(existing.FromPeerID),
			Text:         framed,
			BypassCircle: true,
			Attachments:  req.Attachments,
		})
		if err != nil {
			if errors.Is(err, ErrNotConnected) {
				// Asker has no live WS: keep the ask OPEN for retry, 503.
				writeJSONError(w, http.StatusServiceUnavailable, fmt.Sprintf(
					"Reply delivery failed for %s: %s. Ask remains open; retry when "+
						"the asker reconnects.", existing.FromPeerName, err))
				return
			}
			// CheckAccess failure (asker evicted): close without delivery.
			_, _ = h.ask.asks.Close(ctx, req.CorrelationID, "ack_with_msg")
			h.emitAckEvent(ctx, existing, "ack_with_msg", false, true, len(req.Attachments) > 0)
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
			return
		}
		if !res.Delivered() {
			// Queued / not delivered → fail loud, leave the ask open for retry.
			writeJSONError(w, http.StatusServiceUnavailable, fmt.Sprintf(
				"Reply delivery failed for %s: %s. Ask remains open; retry when "+
					"the asker reconnects.", existing.FromPeerName, res.Reason))
			return
		}
		if req.Message != nil {
			h.ask.asks.CaptureReply(ctx, req.CorrelationID, *req.Message, nil)
		}
		_, _ = h.ask.asks.Close(ctx, req.CorrelationID, "ack_with_msg")
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}

	// Bare ack.
	h.emitAckEvent(ctx, existing, "ack", false, false, false)
	_, _ = h.ask.asks.Close(ctx, req.CorrelationID, "ack")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ----------------------------------------------------------------------------
// POST /answer
// ----------------------------------------------------------------------------

// AnswerRequest mirrors asks.py AnswerRequest.
type AnswerRequest struct {
	CorrelationID string           `json:"correlation_id"`
	OptionID      *string          `json:"option_id,omitempty"`
	Text          *string          `json:"text,omitempty"`
	Outcome       string           `json:"outcome,omitempty"`
	Message       *string          `json:"message,omitempty"`
	Attachments   []map[string]any `json:"attachments,omitempty"`
}

// handleAnswer answers a structured-question ask: 404 unknown, 422 plain ask
// (use /ack) or invalid option, 410 already answered/closed. Records the typed
// Answer (resolving any blocking waiter), then best-effort notifies a
// human-readable form back to the asker.
func (h *Hub) handleAnswer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	if !h.askReady(w) {
		return
	}
	var req AnswerRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	h.answerInternal(w, r, req)
}

// answerInternal is the shared /answer body, reused by /ack's question delegation.
func (h *Hub) answerInternal(w http.ResponseWriter, r *http.Request, req AnswerRequest) {
	ctx := r.Context()
	existing, ok := h.ask.asks.Get(req.CorrelationID)
	if !ok {
		writeJSONError(w, http.StatusNotFound,
			"No open ask with correlation_id: "+req.CorrelationID)
		return
	}
	if existing.Question == nil {
		writeJSONError(w, http.StatusUnprocessableEntity, fmt.Sprintf(
			"Ask %s is not a structured question; use /ack.", req.CorrelationID))
		return
	}
	outcome := req.Outcome
	if outcome == "" {
		outcome = "answered"
	}
	ans := Answer{
		Outcome:  outcome,
		OptionID: req.OptionID,
		Text:     req.Text,
		Message:  req.Message,
	}
	recorded, err := h.ask.asks.Answer(ctx, req.CorrelationID, ans)
	if err != nil {
		if errors.Is(err, ErrAlreadyAnswered) {
			writeJSONError(w, http.StatusGone, fmt.Sprintf(
				"Ask %s is already answered/closed.", req.CorrelationID))
			return
		}
		if errors.Is(err, ErrAskNotFound) {
			writeJSONError(w, http.StatusNotFound,
				"No open ask with correlation_id: "+req.CorrelationID)
			return
		}
		// Validation error (e.g. unknown option_id, choice w/o option).
		writeJSONError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	// Best-effort deliver a human-readable form back to the asker. tool_permission
	// answers carry no body (the decision is consumed by the requesting transport,
	// not pasted to the asker). pull delivery is already satisfied by the waiter.
	body := answerReplyText(recorded, ans)
	isToolPermission := false
	if scope, _ := existing.Question["scope"].(string); scope == "tool_permission" {
		isToolPermission = true
	}
	delivered := false
	if isToolPermission || body == "" {
		delivered = true // nothing to push
	} else if existing.ReplyDelivery == "pull" {
		delivered = true // the resolved waiter returns the recorded answer
	} else {
		framed := fmt.Sprintf("[ack #%s from @%s] %s",
			req.CorrelationID, existing.ToPeerName, body)
		res, derr := h.ask.delivery.Notify(ctx, NotifyParams{
			FromPeer:     string(existing.ToPeerID),
			ToPeer:       string(existing.FromPeerID),
			Text:         framed,
			BypassCircle: true,
			Attachments:  req.Attachments,
		})
		// The answer is already recorded (first-answer-wins); a failed notify-back
		// is logged via the ack event, not surfaced as an error (the answer stands).
		delivered = derr == nil && res.Delivered()
	}
	h.emitAckEvent(ctx, existing, "answered", delivered, body != "", len(req.Attachments) > 0)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// answerReplyText resolves the human-readable reply body for an answer: explicit
// text wins, else the chosen option's title, else the message. Mirrors
// AskService._answer_reply_text.
func answerReplyText(ask *Ask, ans Answer) string {
	if ans.Text != nil && *ans.Text != "" {
		return *ans.Text
	}
	if ans.OptionID != nil && *ans.OptionID != "" && ask.Question != nil {
		if opts, ok := ask.Question["options"].([]any); ok {
			for _, o := range opts {
				if m, ok := o.(map[string]any); ok {
					if id, _ := m["id"].(string); id == *ans.OptionID {
						if title, _ := m["title"].(string); title != "" {
							return title
						}
						return *ans.OptionID
					}
				}
			}
		}
		return *ans.OptionID
	}
	if ans.Message != nil {
		return *ans.Message
	}
	return ""
}

// ----------------------------------------------------------------------------
// POST /query — legacy blocking RPC, ask-based shim (parity default).
// ----------------------------------------------------------------------------

// QueryRequest mirrors messages.py QueryRequest.
type QueryRequest struct {
	FromPeer     *string  `json:"from_peer,omitempty"`
	ToPeer       string   `json:"to_peer"`
	Text         string   `json:"text"`
	Timeout      *float64 `json:"timeout,omitempty"`
	BypassCircle bool     `json:"bypass_circle,omitempty"`
	Circle       *string  `json:"circle,omitempty"`
}

// QueryResponse mirrors messages.py QueryResponse.
type QueryResponse struct {
	Text   *string `json:"text,omitempty"`
	Error  *string `json:"error,omitempty"`
	Status *string `json:"status,omitempty"`
}

// handleQuery is the blocking RPC compat shim: pre-check the target's status
// (BUSY/OFFLINE/unknown short-circuit to {error,status}), then register a
// BLOCKING text-question ask (scope mesh_ask, default_answer outcome=timed_out)
// and WaitForAnswer up to the timeout. Maps outcome → {text|error}. Mirrors
// messages.py query_peer.
func (h *Hub) handleQuery(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	if !h.askReady(w) {
		return
	}
	var req QueryRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	ctx := r.Context()

	target, err := h.ask.reg.GetPeerByName(req.ToPeer, req.Circle)
	if err != nil {
		writeJSON(w, http.StatusOK, QueryResponse{Error: strPtr(err.Error())})
		return
	}
	if target == nil {
		writeJSON(w, http.StatusOK, QueryResponse{Error: strPtr("Unknown peer: " + req.ToPeer)})
		return
	}
	switch target.Status {
	case proto.StatusBusy:
		writeJSON(w, http.StatusOK, QueryResponse{
			Error:  strPtr(fmt.Sprintf("Peer '%s' is busy", req.ToPeer)),
			Status: strPtr(string(proto.StatusBusy)),
		})
		return
	case proto.StatusOffline:
		writeJSON(w, http.StatusOK, QueryResponse{
			Error:  strPtr(fmt.Sprintf("Peer '%s' is offline", req.ToPeer)),
			Status: strPtr(string(proto.StatusOffline)),
		})
		return
	}

	fromPeer := "cli"
	if req.FromPeer != nil && *req.FromPeer != "" {
		fromPeer = *req.FromPeer
	}
	// CLI requests (no explicit from_peer) auto-bypass circles.
	bypass := req.BypassCircle || req.FromPeer == nil
	timeout := defaultQueryTimeout
	if req.Timeout != nil && *req.Timeout > 0 {
		timeout = time.Duration(*req.Timeout * float64(time.Second))
	}

	var fromIDPtr *string
	if from, ferr := h.ask.reg.GetPeerByName(fromPeer, &target.Circle); ferr == nil && from != nil {
		s := string(from.PeerID)
		fromIDPtr = &s
	}
	h.ask.reg.AddEvent(ctx, "query", map[string]any{
		"from": fromPeer, "to": req.ToPeer, "text": req.Text,
		"from_peer_id": fromIDPtr, "to_peer_id": string(target.PeerID), "status": "pending",
	})

	timeoutMsg := "Timeout waiting for " + req.ToPeer
	question := map[string]any{
		"kind":            "text",
		"prompt":          req.Text,
		"blocking":        true,
		"timeout_seconds": timeout.Seconds(),
		"scope":           "mesh_ask",
		"metadata":        map[string]any{"compat": "query"},
		"default_answer":  map[string]any{"outcome": "timed_out", "message": timeoutMsg},
	}

	cid, err := h.ask.asks.Register(ctx, RegisterAskParams{
		FromPeerID:   proto.PeerID(fromPeer),
		FromPeerName: proto.DisplayName(fromPeer),
		ToPeerID:     target.PeerID,
		ToPeerName:   target.DisplayName,
		Text:         req.Text,
		Question:     question,
	})
	if err != nil {
		if errors.Is(err, ErrQuiesced) {
			writeJSON(w, http.StatusOK, QueryResponse{
				Error: strPtr(fmt.Sprintf("Peer %s is mid-switch; retry shortly.", req.ToPeer)),
			})
			return
		}
		writeJSON(w, http.StatusOK, QueryResponse{Error: strPtr(err.Error())})
		return
	}

	if _, derr := h.ask.delivery.DeliverAsk(ctx, DeliverAskParams{
		FromPeer:      fromPeer,
		ToPeer:        string(target.PeerID),
		Text:          req.Text,
		CorrelationID: cid,
		BypassCircle:  bypass,
		Circle:        req.Circle,
		Question:      question,
	}); derr != nil {
		_, _ = h.ask.asks.Close(ctx, cid, "send_failed")
		writeJSON(w, http.StatusOK, QueryResponse{Error: strPtr(derr.Error())})
		return
	}

	defaultAns := Answer{Outcome: "timed_out", Message: &timeoutMsg}
	ans, werr := h.ask.asks.WaitForAnswer(ctx, cid, timeout, &defaultAns)
	if werr != nil {
		writeJSON(w, http.StatusOK, QueryResponse{Error: strPtr(werr.Error())})
		return
	}
	switch ans.Outcome {
	case "timed_out":
		writeJSON(w, http.StatusOK, QueryResponse{Error: strPtr(timeoutMsg)})
	case "cancelled":
		writeJSON(w, http.StatusOK, QueryResponse{Error: strPtr(derefOr(ans.Message, "Query cancelled"))})
	default:
		text := ""
		if ans.Text != nil {
			text = *ans.Text
		} else if ans.Message != nil {
			text = *ans.Message
		} else if ans.OptionID != nil {
			text = *ans.OptionID
		}
		writeJSON(w, http.StatusOK, QueryResponse{Text: strPtr(text)})
	}
}

// ----------------------------------------------------------------------------
// GET /asks/pending — the Stop-hook reminder source.
// ----------------------------------------------------------------------------

// PendingAsk mirrors asks.py PendingAsk.
type PendingAsk struct {
	CorrelationID string `json:"correlation_id"`
	FromPeer      string `json:"from_peer"`
	ToPeer        string `json:"to_peer"`
	Text          string `json:"text"`
	CreatedAt     string `json:"created_at"`
	Direction     string `json:"direction"`
}

// PendingAsksResponse mirrors asks.py PendingAsksResponse.
type PendingAsksResponse struct {
	Asks []PendingAsk `json:"asks"`
}

// handlePendingAsks returns open asks for a peer (newest first). Lookup is by
// exactly one of pane_id or peer_id (400 otherwise); 404 if the peer is unknown.
// direction ∈ {inbound(default),outbound,both}. Mirrors asks.py pending_asks.
func (h *Hub) handlePendingAsks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	if !h.askReady(w) {
		return
	}
	q := r.URL.Query()
	paneID := q.Get("pane_id")
	peerID := q.Get("peer_id")
	direction := q.Get("direction")
	if direction == "" {
		direction = "inbound"
	}

	if paneID == "" && peerID == "" {
		writeJSONError(w, http.StatusBadRequest, "Must provide pane_id or peer_id")
		return
	}
	if paneID != "" && peerID != "" {
		writeJSONError(w, http.StatusBadRequest, "Provide only one of pane_id or peer_id")
		return
	}
	switch direction {
	case "inbound", "outbound", "both":
	default:
		writeJSONError(w, http.StatusBadRequest, "direction must be one of: inbound, outbound, both")
		return
	}

	var resolved proto.PeerID
	if paneID != "" {
		p, ok := h.ask.reg.GetPeerByPane(paneID)
		if !ok {
			writeJSONError(w, http.StatusNotFound, "No peer for pane: "+paneID)
			return
		}
		resolved = p.PeerID
	} else {
		p, ok := h.ask.reg.GetPeer(proto.PeerID(peerID))
		if !ok {
			writeJSONError(w, http.StatusNotFound, "No peer with id: "+peerID)
			return
		}
		resolved = p.PeerID
	}

	// maxResults<0 → uncapped, matching the Python default (no cap on the poll).
	pending, err := h.ask.asks.PendingForPeer(r.Context(), resolved, -1, direction)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	out := PendingAsksResponse{Asks: make([]PendingAsk, 0, len(pending))}
	for _, ask := range pending {
		dir := "outbound"
		if ask.ToPeerID == resolved {
			dir = "inbound"
		}
		out.Asks = append(out.Asks, PendingAsk{
			CorrelationID: ask.CorrelationID,
			FromPeer:      string(ask.FromPeerName),
			ToPeer:        string(ask.ToPeerName),
			Text:          ask.Text,
			CreatedAt:     ask.CreatedAt.Format(time.RFC3339Nano),
			Direction:     dir,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// ----------------------------------------------------------------------------
// /asks/{correlation_id}/{picked_up|mark_reminded|wait}
// ----------------------------------------------------------------------------

// AskWaitRequest mirrors asks.py AskWaitRequest.
type AskWaitRequest struct {
	PeerID         string   `json:"peer_id"`
	TimeoutSeconds *float64 `json:"timeout_seconds,omitempty"`
}

// AskWaitResponse mirrors asks.py AskWaitResponse.
type AskWaitResponse struct {
	CorrelationID string           `json:"correlation_id"`
	Status        string           `json:"status"` // "resolved" | "pending"
	Reply         *string          `json:"reply,omitempty"`
	Outcome       *string          `json:"outcome,omitempty"`
	OptionID      *string          `json:"option_id,omitempty"`
	Message       *string          `json:"message,omitempty"`
	CloseReason   *string          `json:"close_reason,omitempty"`
	Responder     *string          `json:"responder,omitempty"`
	Attachments   []map[string]any `json:"attachments,omitempty"`
}

// handleAskSubpath dispatches the /asks/{cid}/{action} endpoints. picked_up and
// mark_reminded are deprecated silent no-op 200s (transport compat: the daemon
// no longer tracks pickup state). wait is the bounded wait_on_ack primitive.
func (h *Hub) handleAskSubpath(w http.ResponseWriter, r *http.Request) {
	// Path is /asks/{cid}/{action}; trim the "/asks/" prefix and split.
	rest := strings.TrimPrefix(r.URL.Path, "/asks/")
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) != 2 || parts[0] == "" {
		writeJSONError(w, http.StatusNotFound, "not found")
		return
	}
	cid := parts[0]
	action := parts[1]

	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}

	switch action {
	case "picked_up", "mark_reminded":
		// Deprecated no-op kept for one release so older transports don't 404.
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	case "wait":
		h.handleAskWait(w, r, cid)
	default:
		writeJSONError(w, http.StatusNotFound, "not found")
	}
}

// handleAskWait blocks (bounded) until the ask resolves; pending on timeout. Only
// the original asker may wait (waiting flips the ask to pull delivery): 404 if
// the ask is unknown, 403 if peer_id is neither the asker's id nor name. Clamps
// the wait to askWaitMaxSeconds. Mirrors asks.py wait_on_ask.
func (h *Hub) handleAskWait(w http.ResponseWriter, r *http.Request, cid string) {
	if !h.askReady(w) {
		return
	}
	var req AskWaitRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	ctx := r.Context()

	existing, ok := h.ask.asks.Get(cid)
	if !ok {
		writeJSONError(w, http.StatusNotFound, "No ask with correlation_id: "+cid)
		return
	}
	if req.PeerID != string(existing.FromPeerID) && req.PeerID != string(existing.FromPeerName) {
		writeJSONError(w, http.StatusForbidden, map[string]any{
			"error":          "not_the_asker",
			"correlation_id": cid,
			"asker":          string(existing.FromPeerName),
		})
		return
	}

	timeout := askWaitDefaultSeconds
	if req.TimeoutSeconds != nil {
		timeout = time.Duration(*req.TimeoutSeconds * float64(time.Second))
	}
	if timeout > askWaitMaxSeconds {
		timeout = askWaitMaxSeconds
	}
	if timeout < 0 {
		timeout = 0
	}

	ask, err := h.ask.asks.WaitForResolution(ctx, cid, timeout, true)
	if err != nil {
		if errors.Is(err, ErrAskNotFound) {
			writeJSONError(w, http.StatusNotFound, "No ask with correlation_id: "+cid)
			return
		}
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if ask == nil {
		writeJSON(w, http.StatusOK, AskWaitResponse{CorrelationID: cid, Status: "pending"})
		return
	}

	resp := AskWaitResponse{
		CorrelationID: cid,
		Status:        "resolved",
		CloseReason:   strPtr(ask.CloseReason),
		Responder:     strPtr(string(ask.ToPeerName)),
		Attachments:   ask.ReplyAttachments,
	}
	// reply: prefer the captured reply_text, else the answer text.
	if ask.ReplyText != nil {
		resp.Reply = ask.ReplyText
	} else if ask.Answer != nil {
		resp.Reply = ask.Answer.Text
	}
	if ask.Answer != nil {
		resp.Outcome = strPtr(ask.Answer.Outcome)
		resp.OptionID = ask.Answer.OptionID
		resp.Message = ask.Answer.Message
	}
	writeJSON(w, http.StatusOK, resp)
}

// ----------------------------------------------------------------------------
// shared helpers
// ----------------------------------------------------------------------------

// emitAckEvent records the "ack" journal event with the truthful delivered flag,
// mirroring AskService._emit_ack_event. The from/to are swapped relative to the
// ask (the acker is the ask recipient replying to the original asker).
func (h *Hub) emitAckEvent(ctx context.Context, ask *Ask, reason string, delivered, hasMessage, hasAttachments bool) {
	h.ask.reg.AddEvent(ctx, "ack", map[string]any{
		"from":            string(ask.ToPeerName),
		"to":              string(ask.FromPeerName),
		"from_peer_id":    string(ask.ToPeerID),
		"to_peer_id":      string(ask.FromPeerID),
		"correlation_id":  ask.CorrelationID,
		"status":          reason,
		"delivered":       delivered,
		"has_message":     hasMessage,
		"has_attachments": hasAttachments,
	})
}

func derefOr(p *string, fallback string) string {
	if p != nil {
		return *p
	}
	return fallback
}
