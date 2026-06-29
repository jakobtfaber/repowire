package state

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/repowire/repowire/daemon-go/peer"
	"github.com/repowire/repowire/daemon-go/proto"
)

// realDDL mirrors the schema-v12 tables this package reads, copied verbatim from
// repowire/daemon/state/database.py. It stamps user_version=12 so NewStore opens.
const realDDL = `
CREATE TABLE IF NOT EXISTS peer_session_mappings (
    session_id TEXT PRIMARY KEY,
    display_name TEXT NOT NULL,
    circle TEXT NOT NULL,
    backend TEXT NOT NULL,
    path TEXT,
    role TEXT NOT NULL,
    updated_at TEXT,
    description TEXT NOT NULL DEFAULT '',
    model TEXT,
    agent_pid INTEGER
);
CREATE TABLE IF NOT EXISTS retired_peers (
    peer_id TEXT PRIMARY KEY,
    retired_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS events (
    event_id TEXT PRIMARY KEY,
    type TEXT NOT NULL,
    timestamp TEXT NOT NULL,
    peer_id TEXT,
    peer_name TEXT,
    session_id TEXT,
    turn_id TEXT,
    payload_json TEXT NOT NULL
);
PRAGMA user_version=12;
`

func newTempStore(t *testing.T) *Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "state.db")

	// Create the db with the real DDL using a throwaway connection first.
	seed, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("open seed db: %v", err)
	}
	if _, err := seed.Exec(realDDL); err != nil {
		t.Fatalf("apply DDL: %v", err)
	}
	if err := seed.Close(); err != nil {
		t.Fatalf("close seed db: %v", err)
	}

	s, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestNewStoreRejectsWrongSchemaVersion(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "old.db")
	seed, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	// user_version defaults to 0; do not stamp 12.
	if _, err := seed.Exec(`CREATE TABLE x(a)`); err != nil {
		t.Fatalf("exec: %v", err)
	}
	_ = seed.Close()

	if _, err := NewStore(dbPath); err == nil {
		t.Fatal("expected schema-version mismatch error, got nil")
	}
}

func TestAppendAndReadEvent(t *testing.T) {
	s := newTempStore(t)
	ctx := context.Background()

	ts := time.Date(2026, 6, 29, 12, 34, 56, 789_000_000, time.UTC)
	ev := peer.Event{
		Type:      "peer_online",
		Timestamp: ts,
		PeerID:    proto.PeerID("peer-abc"),
		PeerName:  proto.DisplayName("alice"),
		SessionID: proto.PeerID("peer-abc"),
		Payload:   map[string]any{"reason": "reconnect"},
	}
	if err := s.AppendEvent(ctx, ev); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}

	var (
		eventID   string
		typ       string
		timestamp string
		peerID    sql.NullString
		peerName  sql.NullString
		sessionID sql.NullString
		turnID    sql.NullString
		payload   string
	)
	row := s.db.QueryRowContext(ctx,
		`SELECT event_id, type, timestamp, peer_id, peer_name, session_id, turn_id, payload_json FROM events`)
	if err := row.Scan(&eventID, &typ, &timestamp, &peerID, &peerName, &sessionID, &turnID, &payload); err != nil {
		t.Fatalf("read back event: %v", err)
	}
	if eventID == "" {
		t.Error("event_id should have been generated")
	}
	if typ != "peer_online" {
		t.Errorf("type = %q, want peer_online", typ)
	}
	if timestamp != "2026-06-29T12:34:56.789Z" {
		t.Errorf("timestamp = %q, want 2026-06-29T12:34:56.789Z", timestamp)
	}
	if peerID.String != "peer-abc" {
		t.Errorf("peer_id = %q, want peer-abc", peerID.String)
	}
	if turnID.Valid {
		t.Errorf("turn_id should be NULL, got %q", turnID.String)
	}
	if payload != `{"reason":"reconnect"}` {
		t.Errorf("payload_json = %q", payload)
	}
}

func TestAppendEventEmptyPeerIDStoresNull(t *testing.T) {
	s := newTempStore(t)
	ctx := context.Background()

	if err := s.AppendEvent(ctx, peer.Event{Type: "system"}); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}
	var peerID sql.NullString
	var payload string
	if err := s.db.QueryRowContext(ctx, `SELECT peer_id, payload_json FROM events`).Scan(&peerID, &payload); err != nil {
		t.Fatalf("read: %v", err)
	}
	if peerID.Valid {
		t.Errorf("empty peer_id should be NULL, got %q", peerID.String)
	}
	if payload != "{}" {
		t.Errorf("nil payload should default to {}, got %q", payload)
	}
}

func TestMappingRoundTrip(t *testing.T) {
	s := newTempStore(t)
	ctx := context.Background()

	path := "/work/repo"
	model := "opus"
	pid := 4242
	want := &proto.SessionMapping{
		SessionID:   proto.PeerID("peer-1"),
		DisplayName: proto.DisplayName("bob"),
		Circle:      "default",
		Backend:     proto.AgentType("claude-code"),
		Path:        &path,
		Role:        proto.PeerRole("agent"),
		UpdatedAt:   time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		Description: "a worker",
		Model:       &model,
		AgentPID:    &pid,
	}
	if err := s.UpsertMapping(ctx, want); err != nil {
		t.Fatalf("UpsertMapping: %v", err)
	}

	// A second mapping with NULL path/model/agent_pid.
	bare := &proto.SessionMapping{
		SessionID:   proto.PeerID("peer-2"),
		DisplayName: proto.DisplayName("carol"),
		Circle:      "default",
		Backend:     proto.AgentType("codex"),
		Role:        proto.PeerRole("agent"),
		UpdatedAt:   time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	}
	if err := s.UpsertMapping(ctx, bare); err != nil {
		t.Fatalf("UpsertMapping bare: %v", err)
	}

	got, err := s.LoadMappings(ctx)
	if err != nil {
		t.Fatalf("LoadMappings: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d mappings, want 2", len(got))
	}

	byID := map[proto.PeerID]*proto.SessionMapping{}
	for _, m := range got {
		byID[m.SessionID] = m
	}

	m1 := byID["peer-1"]
	if m1 == nil {
		t.Fatal("peer-1 missing")
	}
	if m1.DisplayName != "bob" || m1.Backend != "claude-code" || m1.Role != "agent" {
		t.Errorf("peer-1 fields wrong: %+v", m1)
	}
	if m1.Path == nil || *m1.Path != path {
		t.Errorf("peer-1 path = %v, want %q", m1.Path, path)
	}
	if m1.Model == nil || *m1.Model != model {
		t.Errorf("peer-1 model = %v", m1.Model)
	}
	if m1.AgentPID == nil || *m1.AgentPID != pid {
		t.Errorf("peer-1 agent_pid = %v", m1.AgentPID)
	}
	if !m1.UpdatedAt.Equal(want.UpdatedAt) {
		t.Errorf("peer-1 updated_at = %v, want %v", m1.UpdatedAt, want.UpdatedAt)
	}

	m2 := byID["peer-2"]
	if m2 == nil {
		t.Fatal("peer-2 missing")
	}
	if m2.Path != nil || m2.Model != nil || m2.AgentPID != nil {
		t.Errorf("peer-2 nullable fields should be nil: %+v", m2)
	}

	// Delete peer-1 and confirm only peer-2 remains.
	if err := s.DeleteMapping(ctx, "peer-1"); err != nil {
		t.Fatalf("DeleteMapping: %v", err)
	}
	got, err = s.LoadMappings(ctx)
	if err != nil {
		t.Fatalf("LoadMappings after delete: %v", err)
	}
	if len(got) != 1 || got[0].SessionID != "peer-2" {
		t.Errorf("after delete, got %+v", got)
	}
}

func TestRetireLoadCutoffAndUnretire(t *testing.T) {
	s := newTempStore(t)
	ctx := context.Background()

	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	recent := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	if err := s.Retire(ctx, "peer-old", old); err != nil {
		t.Fatalf("Retire old: %v", err)
	}
	if err := s.Retire(ctx, "peer-recent", recent); err != nil {
		t.Fatalf("Retire recent: %v", err)
	}

	cutoff := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	got, err := s.LoadRetired(ctx, cutoff)
	if err != nil {
		t.Fatalf("LoadRetired: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d retired, want 1 (cutoff filters old)", len(got))
	}
	if rt, ok := got["peer-recent"]; !ok || !rt.Equal(recent) {
		t.Errorf("peer-recent missing or wrong time: %v %v", ok, rt)
	}
	if _, ok := got["peer-old"]; ok {
		t.Error("peer-old should be filtered by cutoff")
	}

	if err := s.Unretire(ctx, "peer-recent"); err != nil {
		t.Fatalf("Unretire: %v", err)
	}
	got, err = s.LoadRetired(ctx, old)
	if err != nil {
		t.Fatalf("LoadRetired after unretire: %v", err)
	}
	if _, ok := got["peer-recent"]; ok {
		t.Error("peer-recent should be gone after Unretire")
	}
	if _, ok := got["peer-old"]; !ok {
		t.Error("peer-old should remain")
	}
}
