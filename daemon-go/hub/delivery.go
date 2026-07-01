package hub

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/repowire/repowire/daemon-go/proto"
	"github.com/repowire/repowire/daemon-go/state"
)

// ============================================================================
// PeerDelivery — application service that composes registry access-check +
// transport choice (ACP-before-WS) + ask/notify lifecycle + queued-delivery
// fallback. Port of repowire/daemon/peer_delivery.py (PeerDeliveryService).
//
// The WS path is fully specified. The ACP branch is guarded by
// Transport.ACPRoute, which is a stub that always reports "no ACP route" this
// phase, so every target falls through to WS. When the ACP experiment lands the
// branch already exists here (see the `decision, ok := ...ACPRoute(...)` sites).
//
// The router (*MessageRouter) speaks only WS; transport CHOICE lives here. The
// AskTracker owns ask lifecycle; PeerDelivery delivers an already-registered ask
// and, for the (stubbed) ACP completion path, stashes/redelivers replies.
// ============================================================================

// defaultQueueTTLSeconds / defaultQueueMax mirror the Python QueuedDelivery
// store defaults (config.queued_delivery_ttl_seconds / max_per_peer). The daemon
// overrides them via WithQueueConfig; these are the fallback.
const (
	defaultQueueTTLSeconds = 24 * 60 * 60.0 // 24h
	defaultQueueMax        = 50
)

// seedSettleWait / seedSettlePoll mirror seed_gate.py: hold a WS pane injection
// while the recipient is still pending_first_turn, bounded so a seed that never
// settles cannot wedge delivery forever (a possible interleave beats a stall).
const (
	seedSettleWait = 25 * time.Second
	seedSettlePoll = 500 * time.Millisecond
)

// accessRegistry is the subset of *peer.Registry that PeerDelivery calls.
//
// CheckAccess resolves+authorizes a (from,to) pair (the Python ValueError →
// unknown/ambiguous/forbidden is returned as a non-nil error). The rest mirror
// the registry methods the Tier-1 routes use.
//
// ponytail: a narrow seam because the REGISTRY port (which adds CheckAccess /
// GetAllPeers / AddEvent to *peer.Registry) is still in flight. *peer.Registry
// satisfies this once it lands; swap NewPeerDelivery to take *peer.Registry then
// — the bodies don't change. Kept narrow also keeps delivery tests hermetic.
type accessRegistry interface {
	// CheckAccess returns (from, to, err). from is nil for an unknown sender
	// (Python notify behavior: unresolved senders proceed). A non-nil err is the
	// unknown-target / circle-violation / ambiguous-name rejection.
	CheckAccess(ctx context.Context, fromPeer, toPeer string, bypassCircle bool, circle *string) (from, to *proto.Peer, err error)
	GetPeer(id proto.PeerID) (*proto.Peer, bool)
	GetAllPeers() []*proto.Peer
	AddEvent(ctx context.Context, typ string, payload map[string]any) (eventID string)
	MarkOffline(ctx context.Context, id proto.PeerID, terminal bool) (int, error)
}

// queuedDeliveryStore is the durable fallback queue. *state.Store satisfies it.
// When nil, a no-live-transport notify fails loud (returns the TransportError)
// instead of silently dropping.
type queuedDeliveryStore interface {
	EnqueueDelivery(ctx context.Context, d state.QueuedDelivery, ttlSeconds float64, maxPerPeer int, now time.Time) (*state.QueuedDelivery, error)
}

// PeerDelivery coordinates peer-to-peer delivery across the WS path (and, once
// the experiment lands, the ACP path). Mirrors PeerDeliveryService.
type PeerDelivery struct {
	reg       accessRegistry
	router    *MessageRouter
	transport Transport
	asks      *AskTracker
	store     queuedDeliveryStore

	queueTTLSeconds float64
	queueMax        int

	// closeMu/closed/wg/closeCh track the deferBroadcastUntilSeedSettled
	// goroutines so Close can join them without waiting out a 25s seed-gate
	// poll. See Close and awaitSeedSettled.
	closeMu sync.Mutex
	closed  bool
	wg      sync.WaitGroup
	closeCh chan struct{}
}

// NewPeerDelivery wires the delivery service. store may be nil (queued-delivery
// fallback disabled → no-live-transport is a fail-loud error). asks may be nil
// (the scheduled-ask helper then errors).
//
// ponytail: reg is the narrow accessRegistry seam while the REGISTRY port is in
// flight; the authoritative signature is (reg *peer.Registry, router
// *MessageRouter, transport Transport, asks *AskTracker, store *state.Store).
// *peer.Registry / *state.Store satisfy the seams structurally, so collapsing to
// the concrete types later is a signature-only change.
func NewPeerDelivery(reg accessRegistry, router *MessageRouter, transport Transport, asks *AskTracker, store queuedDeliveryStore) *PeerDelivery {
	return &PeerDelivery{
		reg:             reg,
		router:          router,
		transport:       transport,
		asks:            asks,
		store:           store,
		queueTTLSeconds: defaultQueueTTLSeconds,
		queueMax:        defaultQueueMax,
		closeCh:         make(chan struct{}),
	}
}

// WithQueueConfig overrides the queued-delivery ttl/cap (the daemon supplies its
// configured values). Returns the receiver for chaining.
func (d *PeerDelivery) WithQueueConfig(ttlSeconds float64, maxPerPeer int) *PeerDelivery {
	d.queueTTLSeconds = ttlSeconds
	d.queueMax = maxPerPeer
	return d
}

// ----------------------------------------------------------------------------
// Result/param types (delivery-owned, per the authoritative spec).
// ----------------------------------------------------------------------------

// NotifyResult is the explicit fire-and-forget notify outcome
// (NotifyDeliveryResult). Reason is honest about what was proven:
// transport_delivered (live WS write), broker_accepted (ACP prompt dispatched,
// no runtime receipt), queued_delivery (no live transport → durable queue).
type NotifyResult struct {
	Status                string // "sent" | "queued"
	DeliveryState         string // "delivered" | "queued"
	Reason                string // transport_delivered | broker_accepted | queued_delivery
	FromPeerID            *proto.PeerID
	FromPeerName          proto.DisplayName
	ToPeerID              proto.PeerID
	ToPeerName            proto.DisplayName
	HookDelivery          map[string]any
	DeliveryID            string
	Transport             string // "ws" | "acp"
	RepowireSessionID     *string
	FromRepowireSessionID *string
	ToRepowireSessionID   *string
}

// Delivered reports whether the message reached a live transport.
func (r NotifyResult) Delivered() bool { return r.DeliveryState == "delivered" }

// Queued reports whether the message was held in the durable queue.
func (r NotifyResult) Queued() bool { return r.DeliveryState == "queued" }

// AskResult records which transport delivered and the optional hook receipt, so
// callers write truthful delivery-trace stages (pane_injected vs
// injection_failed).
type AskResult struct {
	Transport    string // "ws" | "acp"
	HookDelivery map[string]any
}

// NotifyParams are the inputs to Notify. FromPeer/ToPeer are peer_id or
// display_name as supplied by the caller (CheckAccess resolves them).
type NotifyParams struct {
	FromPeer     string
	ToPeer       string
	Text         string
	BypassCircle bool
	Circle       *string
	Attachments  []map[string]any
	DeliveryID   string
}

// DeliverAskParams are the inputs to DeliverAsk (the ask is ALREADY registered
// in the AskTracker by the caller).
type DeliverAskParams struct {
	FromPeer      string
	ToPeer        string
	Text          string
	CorrelationID string
	ReplyTo       *string
	BypassCircle  bool
	Circle        *string
	Attachments   []map[string]any
	Question      map[string]any
	// OnACPComplete is the ACP reply callback; nil → default (notify the asker /
	// stash on offline). Relevant only when ACPRoute returns a decision.
	OnACPComplete func(ctx context.Context, cid string, reply, errMsg *string)
}

// ----------------------------------------------------------------------------
// Notify
// ----------------------------------------------------------------------------

// Notify resolves+authorizes the target via reg.CheckAccess, seed-gates a
// pending_first_turn WS target, then sends via the chosen transport. On a
// no-live-transport TransportError it marks the peer offline and enqueues to the
// durable queue; if the queue is disabled it returns the error (fail loud →
// 503). deliveryID "" lets the transport mint one.
func (d *PeerDelivery) Notify(ctx context.Context, params NotifyParams) (NotifyResult, error) {
	from, target, err := d.reg.CheckAccess(ctx, params.FromPeer, params.ToPeer, params.BypassCircle, params.Circle)
	if err != nil {
		return NotifyResult{}, err
	}

	fromName := proto.DisplayName(params.FromPeer)
	var fromID *proto.PeerID
	if from != nil {
		fromName = from.DisplayName
		id := from.PeerID
		fromID = &id
	}

	d.gateOnSeedSettled(ctx, target)

	// Transport choice: ACP-before-WS. STUB → ACPRoute always (nil,false), so
	// every target falls through to the WS path below.
	if decision, ok := d.transport.ACPRoute(target); ok && decision != nil {
		// ponytail: ACP notify is fire-and-forget — the broker accepted the
		// prompt task but the runtime receipt is discarded, so the reason is
		// broker_accepted (NOT transport_delivered). The ACP transport fills
		// this in; until then ACPRoute never returns ok and this branch is dead.
		return NotifyResult{
			Status:        "sent",
			DeliveryState: "delivered",
			Reason:        "broker_accepted",
			FromPeerID:    fromID,
			FromPeerName:  fromName,
			ToPeerID:      target.PeerID,
			ToPeerName:    target.DisplayName,
			DeliveryID:    params.DeliveryID,
			Transport:     "acp",
		}, nil
	}

	hookDelivery, err := d.router.SendNotification(
		ctx, fromName, target.PeerID, target.DisplayName,
		proto.DisplayName(params.ToPeer), params.Text, params.Attachments, params.DeliveryID,
	)
	if err != nil {
		if errors.Is(err, ErrNotConnected) {
			return d.queueNotify(ctx, params, fromID, fromName, target, err)
		}
		return NotifyResult{}, err
	}

	return NotifyResult{
		Status:        "sent",
		DeliveryState: "delivered",
		Reason:        "transport_delivered",
		FromPeerID:    fromID,
		FromPeerName:  fromName,
		ToPeerID:      target.PeerID,
		ToPeerName:    target.DisplayName,
		HookDelivery:  hookDelivery,
		DeliveryID:    params.DeliveryID,
		Transport:     "ws",
	}, nil
}

// queueNotify is the no-live-transport fallback: mark the peer offline, enqueue
// to the durable queue, and return a queued result. If the queue is disabled
// (store nil, or EnqueueDelivery returns nil because cap/ttl <= 0) the original
// transport error propagates — fail loud, never silently drop.
func (d *PeerDelivery) queueNotify(
	ctx context.Context,
	params NotifyParams,
	fromID *proto.PeerID,
	fromName proto.DisplayName,
	target *proto.Peer,
	transportErr error,
) (NotifyResult, error) {
	d.markTransportUnreachable(ctx, target, "notify", transportErr)

	if d.store == nil {
		return NotifyResult{}, transportErr
	}
	var fromIDStr *string
	if fromID != nil {
		s := string(*fromID)
		fromIDStr = &s
	}
	attachments := params.Attachments
	if attachments == nil {
		attachments = []map[string]any{}
	}
	queued, err := d.store.EnqueueDelivery(ctx, state.QueuedDelivery{
		PeerID:       string(target.PeerID),
		Kind:         state.DeliveryNotify,
		FromPeerID:   fromIDStr,
		FromPeerName: string(fromName),
		ToPeerName:   string(target.DisplayName),
		Text:         params.Text,
		Attachments:  attachments,
	}, d.queueTTLSeconds, d.queueMax, time.Time{})
	if err != nil {
		return NotifyResult{}, err
	}
	if queued == nil {
		// Queue disabled (cap/ttl <= 0): nothing durable was written. Fail loud.
		return NotifyResult{}, transportErr
	}

	d.reg.AddEvent(ctx, "notification", map[string]any{
		"from":              string(fromName),
		"to":                string(target.DisplayName),
		"text":              params.Text,
		"from_peer_id":      fromIDStr,
		"to_peer_id":        string(target.PeerID),
		"delivery_status":   "queued",
		"delivery_state":    "queued",
		"queue_delivery_id": queued.DeliveryID,
		"attachments":       attachments,
	})

	return NotifyResult{
		Status:        "queued",
		DeliveryState: "queued",
		Reason:        "queued_delivery",
		FromPeerID:    fromID,
		FromPeerName:  fromName,
		ToPeerID:      target.PeerID,
		ToPeerName:    target.DisplayName,
		DeliveryID:    params.DeliveryID,
	}, nil
}

// ----------------------------------------------------------------------------
// DeliverAsk
// ----------------------------------------------------------------------------

// DeliverAsk delivers an ALREADY-REGISTERED ask (caller registers in the
// AskTracker first). CheckAccess → seed-gate → transport choice. A
// *DeliveryInjectionError propagates unchanged (the route records
// injection_failed + 503; the peer is NOT marked unreachable — the socket is
// alive). A genuine TransportError (no live socket) marks the peer offline and
// propagates.
func (d *PeerDelivery) DeliverAsk(ctx context.Context, params DeliverAskParams) (AskResult, error) {
	from, target, err := d.reg.CheckAccess(ctx, params.FromPeer, params.ToPeer, params.BypassCircle, params.Circle)
	if err != nil {
		return AskResult{}, err
	}

	fromName := proto.DisplayName(params.FromPeer)
	if from != nil {
		fromName = from.DisplayName
	}

	d.gateOnSeedSettled(ctx, target)

	if decision, ok := d.transport.ACPRoute(target); ok && decision != nil {
		// ponytail: ACP ask delivery is a daemon-owned background task whose
		// closure is lost on restart; the Python port records a durable
		// acp_ask operation BEFORE dispatch (acp_reconcile.record_acp_ask_operation /
		// settle_acp_ask_operation) so a startup sweep can fail it and notify the
		// asker, and runs OnACPComplete (default: notify the asker, stash the
		// reply on offline). Wire that in when the ACP transport lands; today
		// ACPRoute never returns ok so this branch is dead.
		return AskResult{Transport: "acp"}, nil
	}

	hookDelivery, err := d.router.SendAsk(
		ctx, fromName, target.PeerID, target.DisplayName, proto.DisplayName(params.ToPeer),
		params.CorrelationID, params.Text, params.ReplyTo, params.Question, params.Attachments,
	)
	if err != nil {
		// Injection failure at a live pane: do NOT mark unreachable (fail loud,
		// propagate so the route records injection_failed + 503).
		if _, ok := AsDeliveryInjection(err); ok {
			return AskResult{}, err
		}
		if errors.Is(err, ErrNotConnected) {
			d.markTransportUnreachable(ctx, target, "ask", err)
		}
		return AskResult{}, err
	}

	return AskResult{Transport: "ws", HookDelivery: hookDelivery}, nil
}

// OpenScheduledAsk registers and delivers a scheduled ask, rolling back the
// AskTracker entry on send failure. Job dispatch passes replyDelivery="pull":
// the @jobs sender has no transport, so the executor's ack reply is retained on
// the ask instead of attempting a notify back to a peer that cannot receive it.
func (d *PeerDelivery) OpenScheduledAsk(ctx context.Context, fromPeer, toPeer, text string, circle *string, replyDelivery string) (string, error) {
	if d.asks == nil {
		return "", errors.New("delivery: AskTracker is required to open scheduled asks")
	}
	cid, err := d.asks.Register(ctx, RegisterAskParams{
		FromPeerID:    proto.PeerID(fromPeer),
		FromPeerName:  proto.DisplayName(fromPeer),
		ToPeerID:      proto.PeerID(toPeer),
		ToPeerName:    proto.DisplayName(toPeer),
		Text:          text,
		ReplyDelivery: replyDelivery,
	})
	if err != nil {
		return "", err
	}
	if _, err := d.DeliverAsk(ctx, DeliverAskParams{
		FromPeer:      fromPeer,
		ToPeer:        toPeer,
		Text:          text,
		CorrelationID: cid,
		Circle:        circle,
	}); err != nil {
		_, _ = d.asks.Close(ctx, cid, "send_failed")
		return "", err
	}
	return cid, nil
}

// ----------------------------------------------------------------------------
// Broadcast
// ----------------------------------------------------------------------------

// Broadcast fans out best-effort to all eligible connected peers (circle-gated
// unless the sender bypasses), deferring pending_first_turn WS peers behind the
// seed gate and (when ACPRoute is live) routing ACP peers separately. Returns
// the delivered display-names and per-recipient failures. Mirrors
// PeerDeliveryService.broadcast.
func (d *PeerDelivery) Broadcast(ctx context.Context, fromPeer, text string, exclude []string, bypassCircle bool) (sent []proto.DisplayName, failed []BroadcastFailure) {
	excludeNames := map[string]struct{}{fromPeer: {}}
	for _, n := range exclude {
		excludeNames[n] = struct{}{}
	}

	excludeIDs := map[proto.PeerID]struct{}{}
	// Resolve the sender to learn its circle / bypass status. bypass_circle here
	// so an unknown/cross-circle sender lookup never errors the broadcast.
	fromObj, _, _ := d.reg.CheckAccess(ctx, fromPeer, fromPeer, true, nil)

	peers := d.reg.GetAllPeers()
	idToName := map[proto.PeerID]proto.DisplayName{}
	for _, p := range peers {
		idToName[p.PeerID] = p.DisplayName
		if _, ex := excludeNames[string(p.DisplayName)]; ex {
			excludeIDs[p.PeerID] = struct{}{}
		}
		if _, ex := excludeNames[string(p.PeerID)]; ex {
			excludeIDs[p.PeerID] = struct{}{}
		}
	}

	senderBypasses := fromObj != nil && (bypassCircle || fromObj.Role.BypassesCircles())
	if fromObj != nil && !senderBypasses {
		for _, p := range peers {
			if p.Circle != fromObj.Circle && !p.Role.BypassesCircles() {
				excludeIDs[p.PeerID] = struct{}{}
			}
		}
	}

	var fromIDPtr *string
	if fromObj != nil {
		s := string(fromObj.PeerID)
		fromIDPtr = &s
	}
	d.reg.AddEvent(ctx, "broadcast", map[string]any{
		"from":         fromPeer,
		"text":         text,
		"exclude":      exclude,
		"from_peer_id": fromIDPtr,
	})

	// ACP-routed recipients have no WS session, so router.Broadcast (which
	// iterates live WS sessions) never reaches them. ponytail: fan out to the
	// eligible ACP peers through the same ACP-before-WS path used for notify
	// once the ACP transport lands. Today ACPRoute never returns ok, so no peer
	// is split off here — match WS broadcast semantics (never resurrect OFFLINE).
	for _, p := range peers {
		if _, ex := excludeIDs[p.PeerID]; ex {
			continue
		}
		if p.Status == proto.StatusOffline {
			continue
		}
		if decision, ok := d.transport.ACPRoute(p); ok && decision != nil {
			excludeIDs[p.PeerID] = struct{}{}
		}
	}

	// WS recipients still seeding (pending_first_turn) would have the broadcast
	// interleaved with their in-flight spawn seed: deliver each via a background
	// goroutine that awaits the seed gate first. One pending peer never blocks
	// the rest of the fanout.
	var deferredNames []proto.DisplayName
	for _, p := range peers {
		if _, ex := excludeIDs[p.PeerID]; ex {
			continue
		}
		if p.Status == proto.StatusOffline {
			continue
		}
		if p.TurnState != proto.TurnPendingFirstTurn {
			continue
		}
		excludeIDs[p.PeerID] = struct{}{}
		d.deferBroadcastUntilSeedSettled(fromPeer, text, p.PeerID)
		deferredNames = append(deferredNames, p.DisplayName)
	}

	sentIDs, failures := d.router.Broadcast(ctx, proto.DisplayName(fromPeer), text, excludeIDs)
	for _, id := range sentIDs {
		if name, ok := idToName[id]; ok {
			sent = append(sent, name)
		}
	}
	sent = append(sent, deferredNames...)
	return sent, failures
}

// ----------------------------------------------------------------------------
// Query (legacy blocking RPC)
// ----------------------------------------------------------------------------

// Query is the legacy blocking RPC: CheckAccess → seed-gate → router.SendQuery,
// returning the Stop-hook response text. The /query HTTP route in the Python
// daemon now wraps a blocking-question ask; this keeps the direct SendQuery path
// (the route layer may wrap an ask shim on top).
func (d *PeerDelivery) Query(ctx context.Context, fromPeer, toPeer, text string, timeout time.Duration, bypassCircle bool, circle *string) (string, error) {
	from, target, err := d.reg.CheckAccess(ctx, fromPeer, toPeer, bypassCircle, circle)
	if err != nil {
		return "", err
	}
	fromName := proto.DisplayName(fromPeer)
	if from != nil {
		fromName = from.DisplayName
	}
	formatted := fmt.Sprintf(
		"[Repowire Query from @%s]\n%s\n\n"+
			"IMPORTANT: Respond directly in your message. Do NOT use ask() to reply - "+
			"your response is automatically captured and returned to %s.",
		fromPeer, text, fromPeer,
	)
	d.gateOnSeedSettled(ctx, target)
	return d.router.SendQuery(ctx, fromName, target.PeerID, target.DisplayName, formatted, timeout)
}

// ----------------------------------------------------------------------------
// Seed gate + transport-unreachable helpers
// ----------------------------------------------------------------------------

// gateOnSeedSettled holds a WS pane injection while target's spawn seed is in
// flight (target is pending_first_turn). Only WS-routed targets inject into a
// pane — ACP-routed targets prompt the broker (which serializes turns itself) —
// so this is a no-op for ACP. Bounded wait; proceeds anyway on timeout rather
// than re-queueing (there is no flush trigger to re-arm a live delivery).
func (d *PeerDelivery) gateOnSeedSettled(ctx context.Context, target *proto.Peer) {
	if target == nil || target.TurnState != proto.TurnPendingFirstTurn {
		return
	}
	if decision, ok := d.transport.ACPRoute(target); ok && decision != nil {
		return
	}
	d.awaitSeedSettled(ctx, target.PeerID)
}

// awaitSeedSettled polls the registry until the peer leaves pending_first_turn,
// the peer vanishes, the bounded deadline elapses, or ctx is cancelled. Mirrors
// seed_gate.await_seed_settled.
func (d *PeerDelivery) awaitSeedSettled(ctx context.Context, id proto.PeerID) {
	deadline := time.Now().Add(seedSettleWait)
	for {
		p, ok := d.reg.GetPeer(id)
		if !ok || p.TurnState != proto.TurnPendingFirstTurn {
			return
		}
		if time.Now().After(deadline) {
			log.Printf("delivery: %s proceeding while still pending_first_turn (seed not settled within %s)", id, seedSettleWait)
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-d.closeCh:
			return
		case <-time.After(seedSettlePoll):
		}
	}
}

// deferBroadcastUntilSeedSettled schedules a single broadcast to a still-seeding
// WS peer in a background goroutine, tracked so Close can join it. The peer is
// already connected, so the queued-delivery store (which only flushes on ws
// connect) would strand the message — instead we wait out the seed gate and
// send directly. One pending peer must never block the rest of the broadcast
// fanout. No-op after Close (mirrors peer.Registry.spawnTracked's gate).
func (d *PeerDelivery) deferBroadcastUntilSeedSettled(fromPeer, text string, id proto.PeerID) {
	d.closeMu.Lock()
	if d.closed {
		d.closeMu.Unlock()
		return
	}
	d.wg.Add(1)
	d.closeMu.Unlock()
	go func() {
		defer d.wg.Done()
		ctx := context.Background()
		d.awaitSeedSettled(ctx, id)
		if err := d.router.BroadcastToSession(ctx, proto.DisplayName(fromPeer), text, id); err != nil {
			log.Printf("delivery: deferred broadcast to %s failed after seed gate: %v", id, err)
		}
	}()
}

// Close unblocks any goroutine parked in awaitSeedSettled's seed-gate poll
// (up to seedSettleWait=25s) and joins them. Call before the registry/store
// shut down — a deferred broadcast that outlives them would read/write closed
// state.
func (d *PeerDelivery) Close() {
	d.closeMu.Lock()
	if !d.closed {
		d.closed = true
		close(d.closeCh)
	}
	d.closeMu.Unlock()
	d.wg.Wait()
}

// markTransportUnreachable drives a peer offline after a genuine TransportError
// (no live socket). It skips repowire_cli_fallback peers (a CLI peer with no
// runtime should not be retired on a failed push). Best-effort; mirrors
// PeerDeliveryService._mark_transport_unreachable.
func (d *PeerDelivery) markTransportUnreachable(ctx context.Context, target *proto.Peer, operation string, transportErr error) {
	if target == nil {
		return
	}
	if target.Metadata != nil {
		if v, ok := target.Metadata["repowire_cli_fallback"].(bool); ok && v {
			return
		}
	}
	if _, err := d.reg.MarkOffline(ctx, target.PeerID, false); err != nil {
		log.Printf("delivery: mark %s offline after %s transport failure failed: %v", target.PeerID, operation, err)
		return
	}
	log.Printf("delivery: marked peer %s offline after %s transport failure: %v", target.PeerID, operation, transportErr)
}
