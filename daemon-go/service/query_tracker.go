package service

import (
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/repowire/repowire/daemon-go/proto"
)

// ErrPeerDisconnected resolves a pending query whose target peer dropped its
// socket before answering. Mirrors the Python PeerDisconnectedError path.
var ErrPeerDisconnected = errors.New("hub: target peer disconnected before responding")

// queryResult carries either the answer text or the error a pending query
// resolves with. Exactly one of Text / Err is meaningful.
type queryResult struct {
	Text string
	Err  error
}

// PendingQuery is a query awaiting a response. ToPeerID is the routing key
// (PeerID); ToPeerName / FromPeer are addressing only. Future delivers the
// single result.
type PendingQuery struct {
	CorrelationID string
	FromPeer      proto.DisplayName
	ToPeerID      proto.PeerID
	ToPeerName    proto.DisplayName
	QueryText     string
	CreatedAt     time.Time
	Future        chan queryResult
}

// QueryTracker is the slim pending-query store: correlation_id -> query, plus a
// peer_id -> {correlation_id} index so a disconnect cancels every query routed
// to that peer. Routing-sensitive indexing is by PeerID, never DisplayName.
type QueryTracker struct {
	mu      sync.RWMutex
	queries map[string]*PendingQuery
	byPeer  map[proto.PeerID]map[string]struct{}
}

// NewQueryTracker returns an empty tracker.
func NewQueryTracker() *QueryTracker {
	return &QueryTracker{
		queries: make(map[string]*PendingQuery),
		byPeer:  make(map[proto.PeerID]map[string]struct{}),
	}
}

// RegisterQuery records a pending query and returns its generated correlation_id.
// Must be called BEFORE sending so the Future exists before any response lands.
func (q *QueryTracker) RegisterQuery(from proto.DisplayName, to proto.PeerID, toName proto.DisplayName, text string) string {
	corrID := uuid.NewString()
	pq := &PendingQuery{
		CorrelationID: corrID,
		FromPeer:      from,
		ToPeerID:      to,
		ToPeerName:    toName,
		QueryText:     text,
		CreatedAt:     time.Now().UTC(),
		Future:        make(chan queryResult, 1),
	}
	q.mu.Lock()
	q.queries[corrID] = pq
	if q.byPeer[to] == nil {
		q.byPeer[to] = make(map[string]struct{})
	}
	q.byPeer[to][corrID] = struct{}{}
	q.mu.Unlock()
	return corrID
}

// Future returns the result channel for a pending query, or nil if unknown.
func (q *QueryTracker) Future(corrID string) chan queryResult {
	q.mu.RLock()
	defer q.mu.RUnlock()
	if pq, ok := q.queries[corrID]; ok {
		return pq.Future
	}
	return nil
}

// ResolveQuery delivers a response text to the awaiting query and removes it.
func (q *QueryTracker) ResolveQuery(corrID, text string) {
	pq := q.take(corrID)
	if pq != nil {
		pq.Future <- queryResult{Text: text}
	}
}

// ResolveOldestQuery resolves the oldest pending query routed to id with text,
// returning whether one was found. The /response endpoint uses this when a Stop
// hook delivers a response without a correlation_id — the daemon matches it to
// the longest-waiting query to that peer. Mirrors QueryTracker.resolve_oldest_query.
func (q *QueryTracker) ResolveOldestQuery(id proto.PeerID, text string) bool {
	q.mu.Lock()
	corrIDs := q.byPeer[id]
	var oldest *PendingQuery
	for corrID := range corrIDs {
		pq, ok := q.queries[corrID]
		if !ok {
			continue
		}
		if oldest == nil || pq.CreatedAt.Before(oldest.CreatedAt) {
			oldest = pq
		}
	}
	if oldest == nil {
		q.mu.Unlock()
		return false
	}
	delete(q.queries, oldest.CorrelationID)
	if set, ok := q.byPeer[id]; ok {
		delete(set, oldest.CorrelationID)
		if len(set) == 0 {
			delete(q.byPeer, id)
		}
	}
	q.mu.Unlock()
	oldest.Future <- queryResult{Text: text}
	return true
}

// ResolveQueryError fails a pending query with err and removes it.
func (q *QueryTracker) ResolveQueryError(corrID string, err error) {
	pq := q.take(corrID)
	if pq != nil {
		pq.Future <- queryResult{Err: err}
	}
}

// CancelQueriesToPeer fails every query routed to id with ErrPeerDisconnected.
// MUST be invoked from the ws handler's finally path (and is wired as the
// registry's OnOffline hook) so a dropped socket never strands an awaiter.
func (q *QueryTracker) CancelQueriesToPeer(id proto.PeerID) {
	q.mu.Lock()
	corrIDs := q.byPeer[id]
	delete(q.byPeer, id)
	var doomed []*PendingQuery
	for corrID := range corrIDs {
		if pq, ok := q.queries[corrID]; ok {
			doomed = append(doomed, pq)
			delete(q.queries, corrID)
		}
	}
	q.mu.Unlock()
	for _, pq := range doomed {
		pq.Future <- queryResult{Err: ErrPeerDisconnected}
	}
}

// take removes and returns a pending query, dropping it from the peer index too.
func (q *QueryTracker) take(corrID string) *PendingQuery {
	q.mu.Lock()
	defer q.mu.Unlock()
	pq, ok := q.queries[corrID]
	if !ok {
		return nil
	}
	delete(q.queries, corrID)
	if set, ok := q.byPeer[pq.ToPeerID]; ok {
		delete(set, corrID)
		if len(set) == 0 {
			delete(q.byPeer, pq.ToPeerID)
		}
	}
	return pq
}
