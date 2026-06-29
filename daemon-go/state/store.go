// Package state implements peer.Store against the EXISTING schema-v12 SQLite
// db owned by the Python daemon. It never creates or migrates tables; it reads
// and writes the schema as defined in repowire/daemon/state/database.py.
package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"

	"github.com/repowire/repowire/daemon-go/peer"
	"github.com/repowire/repowire/daemon-go/proto"
)

// Compile-time assertion: Store satisfies the peer.Store contract.
var _ peer.Store = (*Store)(nil)

// schemaVersion is the user_version the Python daemon stamps. We refuse to open
// anything else rather than silently corrupting an unexpected schema.
const schemaVersion = 12

// tsLayout is the exact format the Python daemon writes (strftime %Y-%m-%dT%H:%M:%fZ).
const tsLayout = "2006-01-02T15:04:05.000Z"

// tsLayouts are accepted on read; Python writes %f-millisecond Z, but be liberal.
var tsLayouts = []string{
	tsLayout,
	"2006-01-02T15:04:05Z",
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02T15:04:05.000000Z",
}

// Store is the SQLite-backed peer.Store implementation.
type Store struct {
	db *sql.DB
}

// NewStore opens the existing daemon state db read-compatibly. It verifies the
// schema version is exactly 12 and never migrates.
func NewStore(dbPath string) (*Store, error) {
	dsn := fmt.Sprintf(
		"file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)",
		dbPath,
	)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open state db: %w", err)
	}
	// modernc.org/sqlite + WAL: a single writer connection is the simplest
	// correct model and avoids "database is locked" under concurrency.
	db.SetMaxOpenConns(1)

	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("read user_version: %w", err)
	}
	if version != schemaVersion {
		_ = db.Close()
		return nil, fmt.Errorf("state db schema version %d, expected %d (Python daemon owns migrations)", version, schemaVersion)
	}
	return &Store{db: db}, nil
}

// Close closes the underlying connection pool.
func (s *Store) Close() error {
	return s.db.Close()
}

// parseTS tries each accepted layout, returning UTC.
func parseTS(raw string) (time.Time, error) {
	for _, layout := range tsLayouts {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unparseable timestamp %q", raw)
}

// formatTS renders the canonical Python strftime form in UTC.
func formatTS(t time.Time) string {
	return t.UTC().Format(tsLayout)
}

// LoadMappings hydrates every peer_session_mappings row.
func (s *Store) LoadMappings(ctx context.Context) ([]*proto.SessionMapping, error) {
	const q = `SELECT session_id, display_name, circle, backend, path, role, updated_at, description, model, agent_pid FROM peer_session_mappings`
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("load mappings: %w", err)
	}
	defer rows.Close()

	var out []*proto.SessionMapping
	for rows.Next() {
		var (
			sessionID   string
			displayName string
			circle      string
			backend     string
			path        sql.NullString
			role        string
			updatedAt   sql.NullString
			description string
			model       sql.NullString
			agentPID    sql.NullInt64
		)
		if err := rows.Scan(&sessionID, &displayName, &circle, &backend, &path, &role, &updatedAt, &description, &model, &agentPID); err != nil {
			return nil, fmt.Errorf("scan mapping: %w", err)
		}
		m := &proto.SessionMapping{
			SessionID:   proto.PeerID(sessionID),
			DisplayName: proto.DisplayName(displayName),
			Circle:      circle,
			Backend:     proto.AgentType(backend),
			Role:        proto.PeerRole(role),
			Description: description,
		}
		if path.Valid {
			p := path.String
			m.Path = &p
		}
		if model.Valid {
			md := model.String
			m.Model = &md
		}
		if agentPID.Valid {
			pid := int(agentPID.Int64)
			m.AgentPID = &pid
		}
		if updatedAt.Valid && updatedAt.String != "" {
			t, err := parseTS(updatedAt.String)
			if err != nil {
				return nil, fmt.Errorf("mapping %s updated_at: %w", sessionID, err)
			}
			m.UpdatedAt = t
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate mappings: %w", err)
	}
	return out, nil
}

// UpsertMapping persists one mapping row.
func (s *Store) UpsertMapping(ctx context.Context, m *proto.SessionMapping) error {
	const q = `INSERT OR REPLACE INTO peer_session_mappings
		(session_id, display_name, circle, backend, path, role, updated_at, description, model, agent_pid)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	var path any
	if m.Path != nil {
		path = *m.Path
	}
	var model any
	if m.Model != nil {
		model = *m.Model
	}
	var agentPID any
	if m.AgentPID != nil {
		agentPID = *m.AgentPID
	}
	updatedAt := m.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = time.Now()
	}

	_, err := s.db.ExecContext(ctx, q,
		string(m.SessionID),
		string(m.DisplayName),
		m.Circle,
		string(m.Backend),
		path,
		string(m.Role),
		formatTS(updatedAt),
		m.Description,
		model,
		agentPID,
	)
	if err != nil {
		return fmt.Errorf("upsert mapping %s: %w", m.SessionID, err)
	}
	return nil
}

// DeleteMapping removes a mapping by peer_id (session_id column).
func (s *Store) DeleteMapping(ctx context.Context, id proto.PeerID) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM peer_session_mappings WHERE session_id = ?`, string(id))
	if err != nil {
		return fmt.Errorf("delete mapping %s: %w", id, err)
	}
	return nil
}

// LoadRetired returns retired peer_ids whose retired_at >= cutoff.
func (s *Store) LoadRetired(ctx context.Context, cutoff time.Time) (map[proto.PeerID]time.Time, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT peer_id, retired_at FROM retired_peers`)
	if err != nil {
		return nil, fmt.Errorf("load retired: %w", err)
	}
	defer rows.Close()

	out := make(map[proto.PeerID]time.Time)
	for rows.Next() {
		var (
			peerID    string
			retiredAt string
		)
		if err := rows.Scan(&peerID, &retiredAt); err != nil {
			return nil, fmt.Errorf("scan retired: %w", err)
		}
		t, err := parseTS(retiredAt)
		if err != nil {
			return nil, fmt.Errorf("retired %s retired_at: %w", peerID, err)
		}
		if t.Before(cutoff) {
			continue
		}
		out[proto.PeerID(peerID)] = t
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate retired: %w", err)
	}
	return out, nil
}

// Retire records a terminal peer_id.
func (s *Store) Retire(ctx context.Context, id proto.PeerID, at time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO retired_peers (peer_id, retired_at) VALUES (?, ?)`,
		string(id), formatTS(at),
	)
	if err != nil {
		return fmt.Errorf("retire %s: %w", id, err)
	}
	return nil
}

// Unretire clears a retirement.
func (s *Store) Unretire(ctx context.Context, id proto.PeerID) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM retired_peers WHERE peer_id = ?`, string(id))
	if err != nil {
		return fmt.Errorf("unretire %s: %w", id, err)
	}
	return nil
}

// AppendEvent writes one immutable journal row.
func (s *Store) AppendEvent(ctx context.Context, e peer.Event) error {
	eventID := e.EventID
	if eventID == "" {
		eventID = uuid.NewString()
	}

	payload := []byte("{}")
	if e.Payload != nil {
		b, err := json.Marshal(e.Payload)
		if err != nil {
			return fmt.Errorf("marshal event payload: %w", err)
		}
		payload = b
	}

	ts := e.Timestamp
	if ts.IsZero() {
		ts = time.Now()
	}

	var peerID any
	if e.PeerID != "" {
		peerID = string(e.PeerID)
	}
	var peerName any
	if e.PeerName != "" {
		peerName = string(e.PeerName)
	}
	var sessionID any
	if e.SessionID != "" {
		sessionID = string(e.SessionID)
	}

	const q = `INSERT OR REPLACE INTO events
		(event_id, type, timestamp, peer_id, peer_name, session_id, turn_id, payload_json)
		VALUES (?, ?, ?, ?, ?, ?, NULL, ?)`
	_, err := s.db.ExecContext(ctx, q,
		eventID,
		e.Type,
		formatTS(ts),
		peerID,
		peerName,
		sessionID,
		string(payload),
	)
	if err != nil {
		return fmt.Errorf("append event %s: %w", eventID, err)
	}
	return nil
}
