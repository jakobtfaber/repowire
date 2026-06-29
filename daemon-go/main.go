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
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/repowire/repowire/daemon-go/hub"
	"github.com/repowire/repowire/daemon-go/peer"
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

	// (5) NewHubWithTransport wires reg.OnOffline -> tracker.CancelQueriesToPeer
	// internally, so a terminal/transport offline cascades query cancellation
	// without the registry learning the tracker's shape.
	h := hub.NewHubWithTransport(reg, transport, *authToken)

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
