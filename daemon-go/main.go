// Command daemon-go is the Go hub: it wires the SQLite state store into the
// peer registry and serves the WebSocket mesh protocol over HTTP. The
// dependency-inversion seam: peer never imports state; it depends on the
// peer.Store interface, which state satisfies. This file (and hub, which
// imports both) wire the concrete Store into the registry. Routing, identity,
// and lifecycle live in the registry/hub; this file is plumbing.
//
// This file assembles the FULL daemon: every hub service (AskTracker,
// PeerDelivery, messaging, ask-lifecycle, session wiring, spawn, jobs/work,
// schedules, reviews, shares, read deps) is constructed and wired so its route
// group is live rather than a nil-guarded no-op. The wiring order is
// load-bearing (transport → registry → hub → services → reconciliation) and is
// called out at each step.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/repowire/repowire/daemon-go/hub"
	"github.com/repowire/repowire/daemon-go/peer"
	"github.com/repowire/repowire/daemon-go/proto"
	"github.com/repowire/repowire/daemon-go/relay"
	"github.com/repowire/repowire/daemon-go/service"
	"github.com/repowire/repowire/daemon-go/state"
)

// realLiveness implements peer.Liveness against the OS: signal 0 probes the
// process table without delivering a signal, so a nil error means the PID is
// alive (or a zombie we don't own — close enough for ghost eviction).
type realLiveness struct{}

func (realLiveness) PIDAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}

// realPaneProbe implements peer.PaneProbe: runtime evidence for an OFFLINE peer
// is a live agent_pid OR a tmux pane that still exists. agent_pid is the strong
// signal (syscall.Kill(pid,0)); a leftover pane must not keep a dead pid alive.
type realPaneProbe struct{}

func (realPaneProbe) HasRuntimeEvidence(p *proto.Peer) bool {
	if p.AgentPID != nil && *p.AgentPID > 0 {
		return syscall.Kill(*p.AgentPID, 0) == nil
	}
	if p.PaneID == nil || *p.PaneID == "" {
		return false
	}
	// tmux display-message -p -t <pane> '#{pane_pid}' exits non-zero if the pane
	// is gone. Best-effort: any error means "no evidence".
	out, err := exec.Command("tmux", "display-message", "-p", "-t", *p.PaneID, "#{pane_pid}").Output()
	return err == nil && len(strings.TrimSpace(string(out))) > 0
}

// tmuxPaneLister implements hub.PaneLister for the session-closed evidence gate.
// It shells out to `tmux list-panes -a` once; an empty/failed listing returns
// nil, which the gate treats as INCONCLUSIVE (never "everything died"). Mirrors
// repowire.hooks._tmux.list_all_panes (the gate only needs pane_id + session).
type tmuxPaneLister struct{}

func (tmuxPaneLister) ListAllPanes() []hub.PaneInfo {
	out, err := exec.Command("tmux", "list-panes", "-a", "-F", "#{pane_id}\t#{session_name}").Output()
	if err != nil {
		return nil
	}
	var panes []hub.PaneInfo
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			continue
		}
		panes = append(panes, hub.PaneInfo{PaneID: parts[0], Session: parts[1]})
	}
	return panes
}

// realProcessProbe implements peer.ProcessProbe for the destructive pane-claim
// proof: one `ps` snapshot for the ancestor walk, and `tmux display-message` for
// the pane root pid. Both are best-effort — ok=false on any error lets the claim
// through (matching the Python guard's safe default). Mirrors
// repowire.daemon.registry_repair.process_ancestors / tmux_pane_pid.
type realProcessProbe struct{}

func (realProcessProbe) Ancestors(pid int) (map[int]struct{}, bool) {
	out, err := exec.Command("ps", "-axo", "pid=,ppid=").Output()
	if err != nil {
		return nil, false
	}
	parent := make(map[int]int)
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		p, err1 := strconv.Atoi(f[0])
		pp, err2 := strconv.Atoi(f[1])
		if err1 != nil || err2 != nil {
			continue
		}
		parent[p] = pp
	}
	ancestors := make(map[int]struct{})
	cur := pid
	for len(ancestors) < 128 {
		next, ok := parent[cur]
		if !ok || next <= 0 {
			break
		}
		if _, seen := ancestors[next]; seen {
			break // cycle guard
		}
		ancestors[next] = struct{}{}
		cur = next
	}
	return ancestors, true
}

func (realProcessProbe) PaneRootPID(paneID string) (int, bool) {
	out, err := exec.Command("tmux", "display-message", "-t", paneID, "-p", "#{pane_pid}").Output()
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0, false
	}
	return pid, true
}

// ----------------------------------------------------------------------------
// Registry seam adapters.
//
// The hub route groups depend on NARROW registry interfaces (accessRegistry,
// askRoutesRegistry, sessionRegistry, …) that were authored ahead of the
// registry port. *peer.Registry satisfies most of those method sets directly,
// but three method shapes diverge and need a thin shim:
//
//   - AddEvent: registry is AddEvent(typ, data) (no ctx); the seams want
//     AddEvent(ctx, typ, data). We drop the ctx (the registry event log doesn't
//     use it — it's a synchronous in-memory push).
//   - UpdateModelByName / UpdateMetadataByName: registry carries an extra
//     trailing `circle *string`; the sessionRegistry seam omits it. We thread
//     nil (no circle scope), matching the Python by-name updaters' default.
//
// ponytail: this shim exists only because the seams predate the registry port.
// When the hub seams are collapsed onto *peer.Registry's actual signatures (or
// the registry methods are reshaped to match), delete regShim and pass `reg`
// directly. Everything else below already takes the concrete *peer.Registry.
// ----------------------------------------------------------------------------

type regShim struct{ *peer.Registry }

func (s regShim) AddEvent(_ context.Context, typ string, payload map[string]any) string {
	return s.Registry.AddEvent(typ, payload)
}

func (s regShim) UpdateModelByName(ctx context.Context, identifier, model string) (bool, error) {
	return s.Registry.UpdateModelByName(ctx, identifier, model, nil)
}

func (s regShim) UpdateMetadataByName(ctx context.Context, identifier string, metadata map[string]any) (bool, error) {
	return s.Registry.UpdateMetadataByName(ctx, identifier, metadata, nil)
}

// ----------------------------------------------------------------------------
// Reconciliation seam adapters.
//
// reg.WithReconciliation takes peer.AskTracker + peer.PeerDelivery interface
// values, which are NARROWER, StashedAsk-projection variants of the concrete
// *service.AskTracker / *service.PeerDelivery (the hub types return []*service.Ask and a
// NotifyParams-shaped Notify; the peer seams want []peer.StashedAsk and a
// positional Notify). These adapters bridge the two so the OFFLINE→live
// stash-redelivery pass and stash-loss emission run against the real tracker.
//
// ponytail: pure shape conversion. If the peer package later imports the hub
// projection directly (or the hub tracker exposes StashedAsk variants), these
// collapse away.
// ----------------------------------------------------------------------------

// reconcileAsks adapts *service.AskTracker to peer.AskTracker.
type reconcileAsks struct{ t *service.AskTracker }

func (a reconcileAsks) TakePendingRepliesForAsker(asker proto.PeerID) []peer.StashedAsk {
	return toStashed(a.t.TakePendingRepliesForAsker(asker))
}

func (a reconcileAsks) TakeOrphanPendingRepliesMatching(id peer.AskerIdentity, live map[proto.PeerID]struct{}) []peer.StashedAsk {
	return toStashed(a.t.TakeOrphanPendingRepliesMatching(service.AskerIdentity(id), live))
}

func (a reconcileAsks) MarkPendingReplyDelivered(cid string, newFrom *proto.PeerID, reason string) bool {
	return a.t.MarkPendingReplyDelivered(context.Background(), cid, newFrom, reason)
}

func (a reconcileAsks) SnapshotPendingRepliesForPeer(id proto.PeerID) []peer.StashedAsk {
	return toStashed(a.t.SnapshotPendingRepliesForPeer(id))
}

func (a reconcileAsks) SnapshotExpiredPendingReplies() []peer.StashedAsk {
	return toStashed(a.t.SnapshotExpiredPendingReplies())
}

func (a reconcileAsks) EvictExpired(includeStashed bool) int {
	return a.t.EvictExpired(context.Background(), includeStashed)
}

func (a reconcileAsks) ForgetPeer(id proto.PeerID) int {
	return a.t.ForgetPeer(context.Background(), id)
}

// toStashed projects hub Asks to the read-only peer.StashedAsk shape the
// reconciler consumes.
func toStashed(asks []*service.Ask) []peer.StashedAsk {
	if len(asks) == 0 {
		return nil
	}
	out := make([]peer.StashedAsk, 0, len(asks))
	for _, a := range asks {
		s := peer.StashedAsk{
			CorrelationID:  a.CorrelationID,
			FromPeerID:     a.FromPeerID,
			FromPeerName:   a.FromPeerName,
			ToPeerID:       a.ToPeerID,
			ToPeerName:     a.ToPeerName,
			PendingReply:   a.PendingReply,
			PendingReplyAt: a.PendingReplyAt,
		}
		if a.AskerIdentity != nil {
			id := peer.AskerIdentity(*a.AskerIdentity)
			s.AskerIdentity = &id
		}
		out = append(out, s)
	}
	return out
}

// reconcileDelivery adapts *service.PeerDelivery to peer.PeerDelivery (the
// positional Notify the reconciler calls to redeliver a stashed reply).
type reconcileDelivery struct{ d *service.PeerDelivery }

func (r reconcileDelivery) Notify(ctx context.Context, from, to proto.PeerID, text string, bypassCircle bool) error {
	_, err := r.d.Notify(ctx, service.NotifyParams{
		FromPeer:     string(from),
		ToPeer:       string(to),
		Text:         text,
		BypassCircle: bypassCircle,
	})
	return err
}

// defaultDBPath resolves the state DB path: $REPOWIRE_STATE_DB wins, else
// ~/.repowire/state.db, matching the Python daemon's layout.
func defaultDBPath() string {
	if p := os.Getenv("REPOWIRE_STATE_DB"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "state.db"
	}
	return filepath.Join(home, ".repowire", "state.db")
}

// splitCSV splits a comma-separated flag/env value into trimmed, non-empty
// entries. Used for the spawn allowlist until the YAML config loader is ported.
func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	dbPath := flag.String("db", defaultDBPath(), "path to the schema-v12 SQLite state DB ($REPOWIRE_STATE_DB)")
	addr := flag.String("addr", "127.0.0.1:8377", "host:port to serve the hub on")
	authToken := flag.String("auth-token", os.Getenv("REPOWIRE_AUTH_TOKEN"), "shared ws auth token ($REPOWIRE_AUTH_TOKEN); empty disables auth")
	// ponytail: spawn allowlist + per-backend launch commands and relay config
	// come from flags/env here. The Go config loader (~/.repowire/config.yaml →
	// config/models.py: SpawnSettings.{commands,allowed_paths} + RelayConfig.
	// {enabled,url,api_key}) is NOT yet ported, and go.mod carries no YAML dep
	// (adding one just for assembly would be over-engineering). When the config
	// package lands, source these from it and drop the flags. Empty allowlist OR
	// empty commands → spawn disabled (SpawnService.Enabled()==false), which is
	// the safe default; the spawn routes 503 rather than spawn into an
	// unexpected directory.
	spawnPathsFlag := flag.String("spawn-allowed-paths", os.Getenv("REPOWIRE_SPAWN_ALLOWED_PATHS"), "comma-separated spawn allowlist roots ($REPOWIRE_SPAWN_ALLOWED_PATHS); empty disables spawn")
	spawnCommandsFlag := flag.String("spawn-commands", os.Getenv("REPOWIRE_SPAWN_COMMANDS"), "per-backend launch commands as JSON {\"claude-code\":\"...\"} ($REPOWIRE_SPAWN_COMMANDS); empty disables spawn")
	relayURL := flag.String("relay-url", envOr("REPOWIRE_RELAY_URL", "wss://repowire.io"), "relay base url for the /shares proxy ($REPOWIRE_RELAY_URL)")
	relayKey := flag.String("relay-api-key", os.Getenv("REPOWIRE_RELAY_API_KEY"), "relay api key ($REPOWIRE_RELAY_API_KEY); empty leaves /shares as a 503 stub")
	flag.Parse()

	// (1) Open the state store. NewStore refuses any schema != 12 — the Python
	// daemon owns migrations, so we fail loud rather than corrupt an unexpected
	// shape.
	store, err := state.NewStore(*dbPath)
	if err != nil {
		log.Fatalf("open state db %q: %v", *dbPath, err)
	}

	// (2-4) Wiring order is load-bearing: build the transport FIRST so the same
	// transport is both the registry's liveness/sever seam and the socket the
	// hub serves on (ghost eviction must see the live sockets). Then build the
	// registry against it, then wrap registry+transport in the hub.
	ctx := context.Background()
	liveness := realLiveness{}
	transport := service.NewWebSocketTransport()

	reg, err := peer.NewRegistry(ctx, store, liveness, transport)
	if err != nil {
		_ = store.Close()
		log.Fatalf("hydrate registry: %v", err)
	}

	// (5) NewHubWithTransport wires reg.OnOffline -> tracker.CancelQueriesToPeer
	// internally, so a terminal/transport offline cascades query cancellation
	// without the registry learning the tracker's shape. It also builds the
	// QueryTracker + MessageRouter the delivery/session groups need.
	h := hub.NewHubWithTransport(reg, transport, *authToken)
	shim := regShim{reg}
	selfMachine, _ := os.Hostname()

	// (6) Application services. AskTracker is the in-memory open-ask store;
	// PeerDelivery composes registry-access + transport-choice + ask/notify
	// lifecycle + the durable queued-delivery fallback (store satisfies the
	// queue seam). The router (WS-only) is the one the hub minted in step 5.
	asks := service.NewAskTracker(0) // 0 → default 24h TTL (config prune_max_age)
	delivery := service.NewPeerDelivery(shim, h.Router(), transport, asks, store)

	// (7) Reconciliation seams. Inject AskTracker + PeerDelivery (via the shape
	// adapters) so the OFFLINE->live stash-redelivery pass and stash-loss
	// emission are LIVE (no longer the nil/dormant spike path). The PaneProbe is
	// the production ps/tmux runtime-evidence gate; the experiment flag and TTLs
	// are defaults until config wiring lands.
	reg.WithReconciliation(
		reconcileAsks{asks},
		reconcileDelivery{delivery},
		realPaneProbe{},
		peer.ExperimentsConfig{ACPBrokerClient: false},
		30*time.Minute, // stale_busy_timeout (default)
		72*time.Hour,   // prune_max_age (default)
	)
	// Process/tmux probe for the destructive pane-claim proof (hijack guard).
	reg.WithProcessProbe(realProcessProbe{})

	// (8) Spawn area. The SpawnService owns the real tmux controller + durable
	// pane-ownership store (proof for destructive kill/restart). SessionControl
	// shares the SAME *SpawnService instance with the work/jobs runner so an
	// executor acquired for a durable job records ownership the spawn routes can
	// later consult.
	tmuxCtl := service.NewRealTmuxController()
	ownership := service.NewFileOwnership(selfMachine, tmuxCtl.ProbePane)
	// Per-backend launch commands come from -spawn-commands (JSON), which
	// `repowire serve` populates from config.daemon.spawn.commands. An empty/invalid
	// map keeps spawn disabled-by-default (SpawnService.Enabled()==false → 503);
	// allowedPaths gates the rest.
	spawnCommands := map[proto.AgentType]string{}
	if s := *spawnCommandsFlag; s != "" {
		if err := json.Unmarshal([]byte(s), &spawnCommands); err != nil {
			log.Fatalf("parse -spawn-commands JSON: %v", err)
		}
	}
	spawnService := service.NewSpawnService(tmuxCtl, ownership, spawnCommands, splitCSV(*spawnPathsFlag))

	// (9) Work/jobs + scheduler. SessionControl is the executor-acquisition
	// ladder (assigned → reuse → resume → spawn); JobRunner dispatches durable
	// jobs through PeerDelivery.OpenScheduledAsk (reply_delivery=pull). The
	// Scheduler fires one-shot/recurring check-ins off a deadline-driven sleep.
	// ponytail: SessionControl.WithResume is not attached — the resume-safety
	// resolver is its own area and not yet ported, so every spawn-strategy
	// acquisition starts fresh (the safe default). JobRunner.SetSenderPeerID is
	// also unset: the synthetic @jobs service peer isn't registered here, so
	// dispatch asks carry an empty `from` (accessRegistry treats an unresolved
	// sender as allowed, mirroring Python notify behavior).
	sessionControl := service.NewSessionControl(shim, spawnService, store)
	jobRunner := service.NewJobRunner(store, delivery, sessionControl)
	scheduler := service.NewScheduler(store, delivery)

	// (10) Relay/shares config. A nil-or-keyless RelayConfig leaves /shares as
	// the documented degrade (503 POST/DELETE, empty-list GET) — i.e. the 503
	// stub when the relay isn't configured.
	relayCfg := &hub.RelayConfig{
		Enabled: *relayKey != "",
		URL:     *relayURL,
		APIKey:  *relayKey,
	}
	// The relay CLIENT (outbound tunnel to repowire.io). Separate from the /shares
	// proxy above: this is what makes the dashboard/phone reach THIS daemon. nil
	// when relay is disabled. Dials the relay and forwards tunneled requests to the
	// local HTTP surface (127.0.0.1) — same trust model as the Python client.
	var relayClient *relay.Client
	if relayCfg.Enabled {
		relayClient = relay.NewClient(relayCfg.URL, relayCfg.APIKey, selfMachine, "http://"+*addr)
	}

	// (11) Wire EVERY route group onto the hub. Each With* gates a route group
	// that is otherwise a nil-guarded no-op in Routes(); the order is
	// independent (all read from already-built services). traces uses the same
	// *state.Store delivery-trace store.
	h.WithReadDeps(asks, store).
		WithMessaging(delivery, store).
		WithAskLifecycle(asks, delivery, shim).
		WithSessionRoutes(shim, h.Tracker(), store).
		WithSpawn(spawnService, reg, asks, selfMachine).
		WithWork(jobRunner, store).
		WithWorkRegistry(reg).
		WithSchedules(store, scheduler).
		WithShares(relayCfg).
		// forgetSpawnedPane drops a dead pane from the spawn-ownership store so
		// destructivePaneProof can't authorize kill/restart against a reused pane id.
		// clearPaneRuntimeState stays nil — the Go hub does not own hook-side pane
		// runtime files (ponytail: wire when those land).
		WithLifecycle(hub.NewLifecycleHandler(reg, transport, tmuxPaneLister{}, spawnService.Ownership().Forget, nil))
	// Reviews defaults its JSON store at Routes() time if unset; leave it.

	// Surface relay-client status on /health and drive its lazy self-heal there
	// (mirrors the Python /health relay block + ensure_running).
	if relayClient != nil {
		h.WithRelayStatus(func() map[string]any {
			relayClient.EnsureRunning(ctx)
			return relayClient.Status().HealthMap()
		})
	}

	// (12) Start the background dispatch loops (deadline-driven, never polling).
	jobRunner.Start(ctx)
	scheduler.Start(ctx)
	if relayClient != nil {
		relayClient.Start(ctx)
	}

	// (13) Register HTTP/ws handlers.
	mux := http.NewServeMux()
	h.Routes(mux)

	srv := &http.Server{Addr: *addr, Handler: mux}

	// (14) Graceful shutdown on SIGINT/SIGTERM: stop accepting, drain, stop the
	// dispatch loops, close DB.
	shutCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		log.Printf("repowire hub listening on %s (db=%s, auth=%v, spawn=%v, relay=%v)",
			*addr, *dbPath, *authToken != "", spawnService.Enabled(), relayCfg.Enabled)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			jobRunner.Stop()
			scheduler.Stop()
			_ = store.Close()
			log.Fatalf("serve: %v", err)
		}
	case <-shutCtx.Done():
		log.Printf("shutdown signal received, draining...")
		drainCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(drainCtx); err != nil {
			log.Printf("shutdown: %v", err)
		}
	}

	jobRunner.Stop()
	scheduler.Stop()
	if relayClient != nil {
		relayClient.Stop()
	}
	// delivery.Close() before reg.Close() is load-bearing: closing PeerDelivery's
	// closeCh unblocks any deferBroadcastUntilSeedSettled goroutine parked in the
	// (up to 25s) seed-gate poll, so the registry's Close (which joins
	// redelivery/LazyRepairAsync goroutines, some of which call back into
	// delivery) doesn't itself wait out that poll. Both must finish before
	// store.Close() — either can still touch the DB.
	//
	// Known limitation (tracked as repowire-fk4): per-connection WS handler
	// goroutines spawned by HandleWS are NOT drained here — srv.Shutdown stops
	// accepting new connections but does not close already-hijacked sockets. A
	// live WS read loop can still be running when store.Close() returns; closing
	// it requires a hub-level socket sweep, which is out of scope for this fix.
	delivery.Close()
	reg.Close()
	if err := store.Close(); err != nil {
		log.Printf("close state db: %v", err)
	}
	log.Printf("hub stopped")
}
