// Command daemon-go is the Go hub: it wires the SQLite state store into the
// peer registry and serves the WebSocket mesh protocol over HTTP. main is the
// ONLY package that imports both state and peer — that's the dependency-
// inversion seam. peer never imports state; it depends on the peer.Store
// interface, which state satisfies. Routing, identity, and lifecycle live in
// the registry/hub; this file is plumbing.
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/repowire/repowire/daemon-go/hub"
	"github.com/repowire/repowire/daemon-go/peer"
	"github.com/repowire/repowire/daemon-go/proto"
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

func main() {
	dbPath := flag.String("db", defaultDBPath(), "path to the schema-v12 SQLite state DB ($REPOWIRE_STATE_DB)")
	addr := flag.String("addr", "127.0.0.1:8377", "host:port to serve the hub on")
	authToken := flag.String("auth-token", os.Getenv("REPOWIRE_AUTH_TOKEN"), "shared ws auth token ($REPOWIRE_AUTH_TOKEN); empty disables auth")
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
	transport := hub.NewWebSocketTransport()

	reg, err := peer.NewRegistry(ctx, store, liveness, transport)
	if err != nil {
		_ = store.Close()
		log.Fatalf("hydrate registry: %v", err)
	}

	// Inject the lifecycle-reconciliation seams. The PaneProbe is the production
	// ps/tmux runtime-evidence gate; the experiment flag and TTLs are spike
	// defaults (config wiring is a later phase).
	//
	// ponytail: asks (AskTracker) and delivery (PeerDelivery) are nil here — the
	// Go hub has no AskTracker yet, so the OFFLINE->live stash-redelivery pass and
	// stash-loss emission stay dormant (nil-safe). Upgrade path: port hub's
	// AskTracker + PeerDelivery and pass them here; the registry passes already
	// gate on `asks != nil` with zero overhead until then.
	reg.WithReconciliation(
		nil, nil, realPaneProbe{},
		peer.ExperimentsConfig{ACPBrokerClient: false},
		30*time.Minute, // stale_busy_timeout (spike default)
		72*time.Hour,   // prune_max_age (spike default)
	)

	// (5) NewHubWithTransport wires reg.OnOffline -> tracker.CancelQueriesToPeer
	// internally, so a terminal/transport offline cascades query cancellation
	// without the registry learning the tracker's shape.
	h := hub.NewHubWithTransport(reg, transport, *authToken)

	// Attach the tmux-lifecycle hook route group. The transport severs sockets;
	// the tmux lister probes live panes for the session-closed evidence gate.
	//
	// ponytail: forgetSpawnedPane / clearPaneRuntimeState are nil (no-ops) — the
	// Go daemon has not yet ported spawn-ownership tracking or the hook-side pane-
	// runtime-state files. Inject them here when those land.
	h.WithLifecycle(hub.NewLifecycleHandler(reg, transport, tmuxPaneLister{}, nil, nil))

	// (6) Register HTTP/ws handlers.
	mux := http.NewServeMux()
	h.Routes(mux)

	srv := &http.Server{Addr: *addr, Handler: mux}

	// (8) Graceful shutdown on SIGINT/SIGTERM: stop accepting, drain, close DB.
	shutCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		log.Printf("repowire hub listening on %s (db=%s, auth=%v)", *addr, *dbPath, *authToken != "")
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
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

	if err := store.Close(); err != nil {
		log.Printf("close state db: %v", err)
	}
	log.Printf("hub stopped")
}
