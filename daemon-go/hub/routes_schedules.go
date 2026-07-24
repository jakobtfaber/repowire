package hub

// Scheduled check-in HTTP route group. Port of repowire/daemon/routes/schedules.py.
//
//	POST   /schedules         create a one-shot or recurring scheduled check-in
//	GET    /schedules         list schedules (optionally filtered by from_peer)
//	DELETE /schedules/{id}    cancel a pending schedule
//
// Wire shapes match the Python daemon field-for-field (CLI/MCP/bot clients
// depend on them). Create requires exactly one of fire_at|cron (400 otherwise):
// fire_at is parsed as ISO-8601 (naive → UTC); the cron path computes the next
// fire externally (service.NextFireAfter) then hands the resolved time to the store. Bad
// cron / unknown kind → 400. After any mutation the scheduler is woken (the
// notify_changed analogue) so its deadline-driven loop recomputes — never a poll.

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/repowire/repowire/daemon-go/service"
	"github.com/repowire/repowire/daemon-go/state"
)

// scheduleStore is the data layer the routes touch. *state.Store satisfies it.
type scheduleStore interface {
	CreateSchedule(ctx context.Context, fromPeer, toPeer, text string, fireAt time.Time, kind string, circle, cron *string) (*state.Schedule, error)
	ListSchedules(ctx context.Context, fromPeer *string) ([]*state.Schedule, error)
	DeleteSchedule(ctx context.Context, scheduleID string) (*state.Schedule, error)
}

// scheduleWaker is the wake seam (the scheduler's Wake). *Scheduler satisfies it.
type scheduleWaker interface{ Wake() }

// ScheduleRoutes owns the /schedules endpoints. It depends only on the store and
// the scheduler wake — no dependency on the spawn/jobs areas, so it lands in
// parallel.
type ScheduleRoutes struct {
	store     scheduleStore
	scheduler scheduleWaker
}

// NewScheduleRoutes wires the route group.
func NewScheduleRoutes(store scheduleStore, scheduler scheduleWaker) *ScheduleRoutes {
	return &ScheduleRoutes{store: store, scheduler: scheduler}
}

// Register attaches the endpoints to the mux, each wrapped by the hub's auth
// middleware. POST/GET share the "/schedules" pattern (dispatched by method);
// DELETE uses the trailing-id pattern.
func (sr *ScheduleRoutes) Register(mux *http.ServeMux, auth func(http.HandlerFunc) http.HandlerFunc) {
	mux.HandleFunc("/schedules", auth(sr.handleSchedules))
	mux.HandleFunc("/schedules/", auth(sr.handleScheduleByID))
}

// ----------------------------------------------------------------------------
// Wire types — match daemon/routes/schedules.py field-for-field.
// ----------------------------------------------------------------------------

type scheduleCreateRequest struct {
	FromPeer string  `json:"from_peer"`
	ToPeer   string  `json:"to_peer"`
	Text     string  `json:"text"`
	FireAt   *string `json:"fire_at"`
	Cron     *string `json:"cron"`
	Kind     string  `json:"kind"`
	Circle   *string `json:"circle"`
}

type scheduleResponse struct {
	ScheduleID string  `json:"schedule_id"`
	FromPeer   string  `json:"from_peer"`
	ToPeer     string  `json:"to_peer"`
	Text       string  `json:"text"`
	FireAt     string  `json:"fire_at"`
	Kind       string  `json:"kind"`
	Circle     *string `json:"circle"`
	Cron       *string `json:"cron"`
	CreatedAt  string  `json:"created_at"`
}

func scheduleToResponse(s *state.Schedule) scheduleResponse {
	return scheduleResponse{
		ScheduleID: s.ScheduleID,
		FromPeer:   s.FromPeer,
		ToPeer:     s.ToPeer,
		Text:       s.Text,
		FireAt:     s.FireAt,
		Kind:       s.Kind,
		Circle:     s.Circle,
		Cron:       s.Cron,
		CreatedAt:  s.CreatedAt,
	}
}

type scheduleListResponse struct {
	Schedules []scheduleResponse `json:"schedules"`
}

// ----------------------------------------------------------------------------
// Handlers
// ----------------------------------------------------------------------------

// handleSchedules dispatches POST (create) and GET (list) on /schedules.
func (sr *ScheduleRoutes) handleSchedules(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		sr.handleCreate(w, r)
	case http.MethodGet:
		sr.handleList(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (sr *ScheduleRoutes) handleCreate(w http.ResponseWriter, r *http.Request) {
	var req scheduleCreateRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	resp, err := sr.create(r.Context(), req)
	if err != nil {
		writeRouteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (sr *ScheduleRoutes) create(ctx context.Context, req scheduleCreateRequest) (scheduleResponse, error) {
	if sr == nil || sr.store == nil || sr.scheduler == nil {
		return scheduleResponse{}, routeErr(http.StatusServiceUnavailable, "schedules not configured")
	}

	// Exactly one of fire_at|cron. (nil == nil) and (set && set) both rejected.
	if (req.FireAt == nil) == (req.Cron == nil) {
		return scheduleResponse{}, routeErr(http.StatusBadRequest, "provide exactly one of fire_at or cron")
	}

	kind := req.Kind
	if kind == "" {
		kind = "notify"
	}

	var (
		fireAt time.Time
		cron   *string
	)
	if req.Cron != nil {
		// Validate + compute the first fire externally, mirroring create_cron →
		// next_fire_after. Store the normalized cron so reschedule reparses cleanly.
		norm, err := service.ValidateCron(*req.Cron)
		if err != nil {
			return scheduleResponse{}, routeErr(http.StatusBadRequest, err.Error())
		}
		next, err := service.NextFireAfter(norm, time.Now().UTC())
		if err != nil {
			return scheduleResponse{}, routeErr(http.StatusBadRequest, err.Error())
		}
		fireAt = next
		cron = &norm
	} else {
		parsed, err := parseFireAt(*req.FireAt)
		if err != nil {
			return scheduleResponse{}, routeErr(http.StatusBadRequest, err.Error())
		}
		fireAt = parsed
	}

	sched, err := sr.store.CreateSchedule(
		ctx, req.FromPeer, req.ToPeer, req.Text, fireAt, kind, req.Circle, cron,
	)
	if err != nil {
		// Invalid kind (and any other store-side validation) → 400, matching the
		// Python ValueError → HTTPException(400) mapping.
		return scheduleResponse{}, routeErr(http.StatusBadRequest, err.Error())
	}

	sr.scheduler.Wake()
	return scheduleToResponse(sched), nil
}

func (sr *ScheduleRoutes) handleList(w http.ResponseWriter, r *http.Request) {
	var fromPeer *string
	if v := r.URL.Query().Get("from_peer"); v != "" {
		fromPeer = &v
	}
	out, err := sr.list(r.Context(), fromPeer)
	if err != nil {
		writeRouteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (sr *ScheduleRoutes) list(ctx context.Context, fromPeer *string) (scheduleListResponse, error) {
	if sr == nil || sr.store == nil {
		return scheduleListResponse{}, routeErr(http.StatusServiceUnavailable, "schedules not configured")
	}
	scheds, err := sr.store.ListSchedules(ctx, fromPeer)
	if err != nil {
		return scheduleListResponse{}, routeErr(http.StatusInternalServerError, err.Error())
	}
	out := scheduleListResponse{Schedules: make([]scheduleResponse, 0, len(scheds))}
	for _, s := range scheds {
		out.Schedules = append(out.Schedules, scheduleToResponse(s))
	}
	return out, nil
}

// handleScheduleByID handles DELETE /schedules/{id}.
func (sr *ScheduleRoutes) handleScheduleByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	// The "/schedules/" prefix pattern doesn't populate PathValue; strip the
	// prefix manually (the codebase's convention — see /asks/ in
	// routes_ask_lifecycle.go).
	id := strings.TrimPrefix(r.URL.Path, "/schedules/")
	if id == "" {
		writeError(w, http.StatusNotFound, "No schedule: ")
		return
	}
	err := sr.delete(r.Context(), id)
	if err != nil {
		writeRouteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (sr *ScheduleRoutes) delete(ctx context.Context, id string) error {
	if sr == nil || sr.store == nil || sr.scheduler == nil {
		return routeErr(http.StatusServiceUnavailable, "schedules not configured")
	}
	removed, err := sr.store.DeleteSchedule(ctx, id)
	if err != nil {
		return routeErr(http.StatusInternalServerError, err.Error())
	}
	if removed == nil {
		return routeErr(http.StatusNotFound, "No schedule: "+id)
	}
	sr.scheduler.Wake()
	return nil
}

// parseFireAt parses an ISO-8601 fire_at. Mirrors _parse_fire_at: a naive
// datetime (no offset) is treated as UTC; an explicit offset is honored and
// converted to UTC. Returns the parsed UTC time, or an error → 400.
func parseFireAt(raw string) (time.Time, error) {
	// Layouts with an explicit offset/zone first (honored), then naive (→ UTC).
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.UTC(), nil
		}
	}
	for _, layout := range []string{
		"2006-01-02T15:04:05.999999999",
		"2006-01-02T15:04:05",
		"2006-01-02T15:04",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"2006-01-02",
	} {
		if t, err := time.ParseInLocation(layout, raw, time.UTC); err == nil {
			return t, nil
		}
	}
	return time.Time{}, errors.New("fire_at must be ISO-8601")
}
