package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/repowire/repowire/daemon-go/config"
)

func addMCPTool[T any](srv *mcp.Server, name, description string, fn func(context.Context, string, T) (string, error)) {
	mcp.AddTool(srv, &mcp.Tool{Name: name, Description: description}, func(ctx context.Context, req *mcp.CallToolRequest, args T) (*mcp.CallToolResult, any, error) {
		text, err := fn(ctx, callerIdentity(req), args)
		if err != nil {
			return nil, nil, err
		}
		return textResult(text), nil, nil
	})
}

func (h *Hub) mcpLocal(ctx context.Context, method, path string, body any) (map[string]any, error) {
	var encoded []byte
	if body != nil {
		encoded, _ = json.Marshal(body)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(encoded)).WithContext(ctx)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if h.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+h.authToken)
	}
	recorder := httptest.NewRecorder()
	mux := http.NewServeMux()
	h.Routes(mux)
	mux.ServeHTTP(recorder, req)
	var result map[string]any
	_ = json.Unmarshal(recorder.Body.Bytes(), &result)
	if recorder.Code < 200 || recorder.Code >= 300 {
		detail := any(recorder.Body.String())
		if result != nil && result["detail"] != nil {
			detail = result["detail"]
		}
		return nil, fmt.Errorf("%s %s failed (%d): %v", method, path, recorder.Code, detail)
	}
	if result == nil {
		result = map[string]any{}
	}
	return result, nil
}

func jsonResult(value any) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}

func requireMCPAdmin(h *Hub, cfg config.MCPHTTPConfig, caller, tool string) error {
	if caller != mcpDefaultIdentity {
		if peer, _ := h.reg.GetPeerByName(caller, nil); peer != nil {
			return nil
		}
	}
	if cfg.AllowDangerousTools {
		return nil
	}
	return fmt.Errorf("%s is disabled for anonymous HTTP MCP; enable daemon.mcp_http.allow_dangerous_tools or use the local identity shim", tool)
}

type mcpAskArgs struct {
	PeerName    string           `json:"peer_name"`
	Query       string           `json:"query"`
	ReplyTo     string           `json:"reply_to,omitempty"`
	Circle      string           `json:"circle,omitempty"`
	Attachments []map[string]any `json:"attachments,omitempty"`
}
type mcpWaitArgs struct {
	CorrelationID  string `json:"correlation_id"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
}
type mcpAskManyArgs struct {
	PeerNames      []string `json:"peer_names"`
	Query          string   `json:"query"`
	Circle         string   `json:"circle,omitempty"`
	TimeoutSeconds int      `json:"timeout_seconds,omitempty"`
}
type mcpIDArgs struct {
	ID         string `json:"id,omitempty"`
	JobID      string `json:"job_id,omitempty"`
	ParentID   string `json:"parent_id,omitempty"`
	ScheduleID string `json:"schedule_id,omitempty"`
	ShareID    string `json:"share_id,omitempty"`
}
type mcpAckArgs struct {
	CorrelationID string           `json:"correlation_id"`
	Message       *string          `json:"message,omitempty"`
	Attachments   []map[string]any `json:"attachments,omitempty"`
}
type mcpAnswerArgs struct {
	CorrelationID string  `json:"correlation_id"`
	OptionID      *string `json:"option_id,omitempty"`
	Text          *string `json:"text,omitempty"`
}

type mcpJobCreateArgs struct {
	Title             string         `json:"title,omitempty"`
	Kind              string         `json:"kind,omitempty"`
	AssignedPeerID    string         `json:"assigned_peer_id,omitempty"`
	OwnerPeerID       string         `json:"owner_peer_id,omitempty"`
	RepowireSessionID string         `json:"repowire_session_id,omitempty"`
	CorrelationID     string         `json:"correlation_id,omitempty"`
	Circle            string         `json:"circle,omitempty"`
	SourceKind        string         `json:"source_kind,omitempty"`
	SourceID          string         `json:"source_id,omitempty"`
	Scope             string         `json:"scope,omitempty"`
	Visibility        string         `json:"visibility,omitempty"`
	DeadlineAt        string         `json:"deadline_at,omitempty"`
	ExpiresAt         string         `json:"expires_at,omitempty"`
	Prompt            string         `json:"prompt,omitempty"`
	PromptFile        string         `json:"prompt_file,omitempty"`
	Path              string         `json:"path,omitempty"`
	Backend           string         `json:"backend,omitempty"`
	Profile           string         `json:"profile,omitempty"`
	DueAt             string         `json:"due_at,omitempty"`
	Cron              string         `json:"cron,omitempty"`
	ResultSurface     string         `json:"result_surface,omitempty"`
	ProcessScope      string         `json:"process_scope,omitempty"`
	Continuity        string         `json:"continuity,omitempty"`
	Request           map[string]any `json:"request,omitempty"`
	Provenance        map[string]any `json:"provenance,omitempty"`
}
type mcpJobListArgs struct {
	State             string `json:"state,omitempty"`
	OwnerPeerID       string `json:"owner_peer_id,omitempty"`
	CreatedByPeerID   string `json:"created_by_peer_id,omitempty"`
	RepowireSessionID string `json:"repowire_session_id,omitempty"`
	Circle            string `json:"circle,omitempty"`
}
type mcpJobUpdateArgs struct {
	JobID         string         `json:"job_id"`
	State         string         `json:"state"`
	StateReason   string         `json:"state_reason,omitempty"`
	Phase         string         `json:"phase,omitempty"`
	ProgressNote  string         `json:"progress_note,omitempty"`
	ResultSummary string         `json:"result_summary,omitempty"`
	AttemptID     string         `json:"attempt_id,omitempty"`
	Progress      map[string]any `json:"progress,omitempty"`
	ResultData    map[string]any `json:"result_data,omitempty"`
	Error         map[string]any `json:"error,omitempty"`
	Provenance    map[string]any `json:"provenance,omitempty"`
	Artifacts     []any          `json:"artifacts,omitempty"`
}
type mcpJobCancelArgs struct {
	JobID  string `json:"job_id"`
	Reason string `json:"reason,omitempty"`
}

type mcpDescriptionArgs struct {
	Description string `json:"description"`
}
type mcpClaimRoleArgs struct {
	Force bool `json:"force,omitempty"`
}
type mcpSpawnArgs struct {
	Path    string `json:"path"`
	Backend string `json:"backend,omitempty"`
	Profile string `json:"profile,omitempty"`
	Command string `json:"command,omitempty"`
	Circle  string `json:"circle,omitempty"`
	Message string `json:"message,omitempty"`
}
type mcpCircleArgs struct {
	Circle string `json:"circle,omitempty"`
}
type mcpKillArgs struct {
	PeerIdentifier string `json:"peer_identifier"`
	Circle         string `json:"circle,omitempty"`
}
type mcpMarkReviewArgs struct {
	PRURL           string  `json:"pr_url"`
	LastReviewedSHA *string `json:"last_reviewed_sha,omitempty"`
}
type mcpReviewArgs struct {
	PeerName string `json:"peer_name,omitempty"`
}
type mcpScheduleArgs struct {
	ToPeer string `json:"to_peer"`
	Text   string `json:"text"`
	FireAt string `json:"fire_at,omitempty"`
	Cron   string `json:"cron,omitempty"`
	Kind   string `json:"kind,omitempty"`
	Circle string `json:"circle,omitempty"`
}
type mcpScheduleSelfArgs struct {
	Text   string `json:"text"`
	FireAt string `json:"fire_at,omitempty"`
	Cron   string `json:"cron,omitempty"`
	Kind   string `json:"kind,omitempty"`
	Circle string `json:"circle,omitempty"`
}
type mcpScheduleListArgs struct {
	MineOnly    *bool `json:"mine_only,omitempty"`
	IncludeCron bool  `json:"include_cron,omitempty"`
}
type mcpShareArgs struct {
	PeerName    string `json:"peer_name,omitempty"`
	Permissions string `json:"permissions,omitempty"`
	TTLSeconds  *int   `json:"ttl_secs,omitempty"`
}

func registerMCPParityTools(srv *mcp.Server, h *Hub, cfg config.MCPHTTPConfig) {
	addMCPTool(srv, "ask", "Open a non-blocking tracked ask thread with a peer.", func(ctx context.Context, caller string, a mcpAskArgs) (string, error) {
		if err := requireFields("peer_name", a.PeerName, "query", a.Query); err != nil {
			return "", err
		}
		body := map[string]any{"from_peer": caller, "to_peer": a.PeerName, "text": a.Query}
		optional(body, "reply_to", a.ReplyTo)
		optional(body, "circle", a.Circle)
		if len(a.Attachments) > 0 {
			body["attachments"] = a.Attachments
		}
		result, err := h.mcpLocal(ctx, http.MethodPost, "/ask", body)
		return stringValue(result, "correlation_id"), err
	})
	addMCPTool(srv, "wait_on_ack", "Wait for a tracked ask to resolve.", func(ctx context.Context, caller string, a mcpWaitArgs) (string, error) {
		if err := required("correlation_id", a.CorrelationID); err != nil {
			return "", err
		}
		timeout := a.TimeoutSeconds
		if timeout <= 0 {
			timeout = 600
		}
		deadline := time.Now().Add(time.Duration(timeout) * time.Second)
		for time.Now().Before(deadline) {
			remaining := time.Until(deadline).Seconds()
			result, err := h.mcpLocal(ctx, http.MethodPost, "/asks/"+url.PathEscape(a.CorrelationID)+"/wait", map[string]any{"peer_id": caller, "timeout_seconds": min(remaining, 45)})
			if err != nil {
				return "", err
			}
			if stringValue(result, "status") == "resolved" {
				return jsonResult(result), nil
			}
		}
		return jsonResult(map[string]any{"correlation_id": a.CorrelationID, "status": "pending"}), nil
	})
	addMCPTool(srv, "ask_many", "Open one tracked ask per peer and return a parent id.", func(ctx context.Context, caller string, a mcpAskManyArgs) (string, error) {
		if len(a.PeerNames) == 0 {
			return "", fmt.Errorf("peer_names is required")
		}
		if err := required("query", a.Query); err != nil {
			return "", err
		}
		body := map[string]any{"from_peer": caller, "to_peers": a.PeerNames, "text": a.Query, "timeout_seconds": defaultInt(a.TimeoutSeconds, 300)}
		optional(body, "circle", a.Circle)
		result, err := h.mcpLocal(ctx, http.MethodPost, "/ask-many", body)
		if err != nil {
			return "", err
		}
		return stringValue(result, "parent_id"), nil
	})
	addMCPTool(srv, "ask_many_result", "Return the current result of an ask-many fanout.", func(ctx context.Context, _ string, a mcpIDArgs) (string, error) {
		if err := required("parent_id", a.ParentID); err != nil {
			return "", err
		}
		result, err := h.mcpLocal(ctx, http.MethodGet, "/ask-many/"+url.PathEscape(a.ParentID), nil)
		return jsonResult(result), err
	})
	addMCPTool(srv, "ack", "Close an ask, optionally replying to its asker.", func(ctx context.Context, caller string, a mcpAckArgs) (string, error) {
		if err := required("correlation_id", a.CorrelationID); err != nil {
			return "", err
		}
		body := map[string]any{"correlation_id": a.CorrelationID, "from_peer": caller}
		if a.Message != nil {
			body["message"] = *a.Message
		}
		if len(a.Attachments) > 0 {
			body["attachments"] = a.Attachments
		}
		_, err := h.mcpLocal(ctx, http.MethodPost, "/ack", body)
		if err != nil {
			return "", err
		}
		suffix := ""
		if a.Message != nil || len(a.Attachments) > 0 {
			suffix = " with reply"
		}
		return "acked #" + a.CorrelationID + suffix, nil
	})
	addMCPTool(srv, "answer", "Answer a structured question.", func(ctx context.Context, caller string, a mcpAnswerArgs) (string, error) {
		if err := required("correlation_id", a.CorrelationID); err != nil {
			return "", err
		}
		if a.OptionID == nil && a.Text == nil {
			return "", fmt.Errorf("option_id or text is required")
		}
		body := map[string]any{"correlation_id": a.CorrelationID, "from_peer": caller}
		if a.OptionID != nil {
			body["option_id"] = *a.OptionID
		}
		if a.Text != nil {
			body["text"] = *a.Text
		}
		_, err := h.mcpLocal(ctx, http.MethodPost, "/answer", body)
		return "answered #" + a.CorrelationID, err
	})

	addMCPTool(srv, "job_create", "Create a durable tracked work job.", func(ctx context.Context, caller string, a mcpJobCreateArgs) (string, error) {
		if err := required("title", a.Title); err != nil {
			return "", err
		}
		body := structMap(a)
		body["created_by_peer_id"] = caller
		if body["kind"] == nil {
			body["kind"] = "general"
		}
		if body["visibility"] == nil {
			body["visibility"] = "circle"
		}
		if body["request"] == nil {
			body["request"] = map[string]any{}
		}
		result, err := h.mcpLocal(ctx, http.MethodPost, "/jobs", body)
		return jsonResult(result), err
	})
	addMCPTool(srv, "job_list", "List durable jobs as JSON.", func(ctx context.Context, _ string, a mcpJobListArgs) (string, error) {
		q := url.Values{}
		addQuery(q, "state", a.State)
		addQuery(q, "owner_peer_id", a.OwnerPeerID)
		addQuery(q, "created_by_peer_id", a.CreatedByPeerID)
		addQuery(q, "repowire_session_id", a.RepowireSessionID)
		addQuery(q, "circle", a.Circle)
		result, err := h.mcpLocal(ctx, http.MethodGet, withQuery("/jobs", q), nil)
		return jsonResult(result), err
	})
	jobStatus := func(ctx context.Context, _ string, a mcpIDArgs) (string, error) {
		if err := required("job_id", a.JobID); err != nil {
			return "", err
		}
		result, err := h.mcpLocal(ctx, http.MethodGet, "/jobs/"+url.PathEscape(a.JobID)+"/status", nil)
		return jsonResult(result), err
	}
	addMCPTool(srv, "job_status", "Return one job status as JSON.", jobStatus)
	addMCPTool(srv, "job_show", "Alias for job_status.", jobStatus)
	addMCPTool(srv, "job_update", "Update a tracked job lifecycle state.", func(ctx context.Context, _ string, a mcpJobUpdateArgs) (string, error) {
		if err := requireFields("job_id", a.JobID, "state", a.State); err != nil {
			return "", err
		}
		body := structMap(a)
		delete(body, "job_id")
		result, err := h.mcpLocal(ctx, http.MethodPatch, "/jobs/"+url.PathEscape(a.JobID), body)
		return jsonResult(result), err
	})
	addMCPTool(srv, "job_result", "Return a tracked job result as JSON.", func(ctx context.Context, _ string, a mcpIDArgs) (string, error) {
		if err := required("job_id", a.JobID); err != nil {
			return "", err
		}
		result, err := h.mcpLocal(ctx, http.MethodGet, "/jobs/"+url.PathEscape(a.JobID)+"/result", nil)
		return jsonResult(result), err
	})
	addMCPTool(srv, "job_cancel", "Request cancellation for a tracked job.", func(ctx context.Context, caller string, a mcpJobCancelArgs) (string, error) {
		if err := required("job_id", a.JobID); err != nil {
			return "", err
		}
		result, err := h.mcpLocal(ctx, http.MethodPost, "/jobs/"+url.PathEscape(a.JobID)+"/cancel", map[string]any{"requested_by_peer_id": caller, "reason": firstNonempty(a.Reason, "cancel_requested")})
		return jsonResult(result), err
	})

	addMCPTool(srv, "set_description", "Update the caller's dashboard task description.", func(ctx context.Context, caller string, a mcpDescriptionArgs) (string, error) {
		_, err := h.mcpLocal(ctx, http.MethodPost, "/peers/"+url.PathEscape(caller)+"/description", map[string]string{"description": a.Description})
		return "description updated: " + a.Description, err
	})
	addMCPTool(srv, "claim_orchestrator_role", "Claim role=orchestrator for the calling peer.", func(ctx context.Context, caller string, a mcpClaimRoleArgs) (string, error) {
		result, err := h.mcpLocal(ctx, http.MethodPost, "/peers/claim-role", map[string]any{"peer_name": caller, "role": "orchestrator", "force": a.Force})
		return jsonResult(result), err
	})
	addMCPTool(srv, "spawn_peer", "Spawn a local tmux-backed coding peer.", func(ctx context.Context, caller string, a mcpSpawnArgs) (string, error) {
		if err := requireMCPAdmin(h, cfg, caller, "spawn_peer"); err != nil {
			return "", err
		}
		if err := required("path", a.Path); err != nil {
			return "", err
		}
		body := structMap(a)
		if body["circle"] == nil {
			if peer, _ := h.reg.GetPeerByName(caller, nil); peer != nil {
				body["circle"] = peer.Circle
			}
		}
		result, err := h.mcpLocal(ctx, http.MethodPost, "/spawn", body)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Spawned %s (tmux: %s) peer_id=%s registration_state=%s", stringValue(result, "display_name"), stringValue(result, "tmux_session"), stringValue(result, "peer_id"), stringValue(result, "registration_state")), nil
	})
	addMCPTool(srv, "orchestrator_status", "Check whether a live orchestrator is present in a circle.", func(ctx context.Context, caller string, a mcpCircleArgs) (string, error) {
		circle := a.Circle
		if circle == "" {
			if peer, _ := h.reg.GetPeerByName(caller, nil); peer != nil {
				circle = peer.Circle
			} else {
				circle = "global"
			}
		}
		result, err := h.mcpLocal(ctx, http.MethodGet, "/circles/"+url.PathEscape(circle)+"/orchestrator", nil)
		return jsonResult(result), err
	})
	addMCPTool(srv, "kill_peer", "Deregister a peer and kill its pane only with destructive proof.", func(ctx context.Context, caller string, a mcpKillArgs) (string, error) {
		if err := requireMCPAdmin(h, cfg, caller, "kill_peer"); err != nil {
			return "", err
		}
		if err := required("peer_identifier", a.PeerIdentifier); err != nil {
			return "", err
		}
		body := map[string]any{"peer_identifier": a.PeerIdentifier, "from_peer": caller}
		optional(body, "circle", a.Circle)
		result, err := h.mcpLocal(ctx, http.MethodPost, "/kill-peer", body)
		return jsonResult(result), err
	})
	addMCPTool(srv, "mark_reviewed", "Record that the caller reviewed a pull request.", func(ctx context.Context, caller string, a mcpMarkReviewArgs) (string, error) {
		if err := required("pr_url", a.PRURL); err != nil {
			return "", err
		}
		body := map[string]any{"reviewer": caller, "pr_url": a.PRURL}
		if a.LastReviewedSHA != nil {
			body["last_reviewed_sha"] = *a.LastReviewedSHA
		}
		_, err := h.mcpLocal(ctx, http.MethodPost, "/reviews", body)
		return "marked reviewed: " + a.PRURL, err
	})
	addMCPTool(srv, "review_queue", "List pull requests awaiting review.", func(ctx context.Context, caller string, a mcpReviewArgs) (string, error) {
		result, err := h.mcpLocal(ctx, http.MethodGet, "/reviews?reviewer="+url.QueryEscape(firstNonempty(a.PeerName, caller)), nil)
		if err != nil {
			return "", err
		}
		lines := []string{"pr_url\tlast_reviewed_sha\tcurrent_head_sha\tstate\tmy_action"}
		for _, raw := range anySlice(result["reviews"]) {
			item, _ := raw.(map[string]any)
			lines = append(lines, strings.Join([]string{stringValue(item, "pr_url"), stringValue(item, "last_reviewed_sha"), stringValue(item, "current_head_sha"), stringValue(item, "state"), stringValue(item, "my_action")}, "\t"))
		}
		return strings.Join(lines, "\n"), nil
	})

	addMCPTool(srv, "schedule_create", "Schedule a one-shot future peer message.", scheduleTool(h, cfg, false))
	addMCPTool(srv, "schedule_cron", "Schedule a recurring peer message.", scheduleTool(h, cfg, true))
	addMCPTool(srv, "schedule_self", "Schedule a future message to the caller.", func(ctx context.Context, caller string, a mcpScheduleSelfArgs) (string, error) {
		if err := requireMCPAdmin(h, cfg, caller, "schedule_self"); err != nil {
			return "", err
		}
		if err := required("text", a.Text); err != nil {
			return "", err
		}
		if (a.FireAt == "") == (a.Cron == "") {
			return "", fmt.Errorf("provide exactly one of fire_at or cron")
		}
		body := map[string]any{"from_peer": caller, "to_peer": caller, "text": a.Text, "kind": firstNonempty(a.Kind, "notify")}
		optional(body, "fire_at", a.FireAt)
		optional(body, "cron", a.Cron)
		optional(body, "circle", a.Circle)
		result, err := h.mcpLocal(ctx, http.MethodPost, "/schedules", body)
		return stringValue(result, "schedule_id"), err
	})
	addMCPTool(srv, "schedule_list", "List pending scheduled messages.", func(ctx context.Context, caller string, a mcpScheduleListArgs) (string, error) {
		path := "/schedules"
		if a.MineOnly == nil || *a.MineOnly {
			path += "?from_peer=" + url.QueryEscape(caller)
		}
		result, err := h.mcpLocal(ctx, http.MethodGet, path, nil)
		if err != nil {
			return "", err
		}
		header := "schedule_id\tfrom_peer\tto_peer\tkind\tfire_at\ttext"
		if a.IncludeCron {
			header += "\tcron"
		}
		lines := []string{header}
		for _, raw := range anySlice(result["schedules"]) {
			item, _ := raw.(map[string]any)
			fields := []string{stringValue(item, "schedule_id"), stringValue(item, "from_peer"), stringValue(item, "to_peer"), stringValue(item, "kind"), stringValue(item, "fire_at"), strings.NewReplacer("\t", " ", "\n", " ").Replace(stringValue(item, "text"))}
			if a.IncludeCron {
				fields = append(fields, stringValue(item, "cron"))
			}
			lines = append(lines, strings.Join(fields, "\t"))
		}
		return strings.Join(lines, "\n"), nil
	})
	addMCPTool(srv, "schedule_delete", "Cancel a pending schedule.", func(ctx context.Context, caller string, a mcpIDArgs) (string, error) {
		if err := requireMCPAdmin(h, cfg, caller, "schedule_delete"); err != nil {
			return "", err
		}
		if err := required("schedule_id", a.ScheduleID); err != nil {
			return "", err
		}
		_, err := h.mcpLocal(ctx, http.MethodDelete, "/schedules/"+url.PathEscape(a.ScheduleID), nil)
		return "deleted schedule " + a.ScheduleID, err
	})
	addMCPTool(srv, "share_session", "Generate a relay share link for a peer.", func(ctx context.Context, caller string, a mcpShareArgs) (string, error) {
		target := firstNonempty(a.PeerName, caller)
		body := map[string]any{"peer_name": target, "permissions": firstNonempty(a.Permissions, "ro"), "ttl_secs": a.TTLSeconds}
		result, err := h.mcpLocal(ctx, http.MethodPost, "/shares", body)
		if err != nil {
			return "", err
		}
		expires := "never"
		if value, ok := result["expires_at"].(string); ok && value != "" {
			expires = value
		}
		return fmt.Sprintf("share link for %s [%s]: %s\nshare_id: %s\nexpires: %s", target, stringValue(result, "permissions"), stringValue(result, "url"), stringValue(result, "share_id"), expires), nil
	})
	addMCPTool(srv, "revoke_share", "Revoke a relay share link.", func(ctx context.Context, _ string, a mcpIDArgs) (string, error) {
		if err := required("share_id", a.ShareID); err != nil {
			return "", err
		}
		_, err := h.mcpLocal(ctx, http.MethodDelete, "/shares/"+url.PathEscape(a.ShareID), nil)
		return "revoked share " + a.ShareID, err
	})
}

func scheduleTool(h *Hub, cfg config.MCPHTTPConfig, cron bool) func(context.Context, string, mcpScheduleArgs) (string, error) {
	return func(ctx context.Context, caller string, a mcpScheduleArgs) (string, error) {
		name := "schedule_create"
		if cron {
			name = "schedule_cron"
		}
		if err := requireMCPAdmin(h, cfg, caller, name); err != nil {
			return "", err
		}
		if err := requireFields("to_peer", a.ToPeer, "text", a.Text); err != nil {
			return "", err
		}
		if cron && a.Cron == "" {
			return "", fmt.Errorf("cron is required")
		}
		if !cron && a.FireAt == "" {
			return "", fmt.Errorf("fire_at is required")
		}
		body := map[string]any{"from_peer": caller, "to_peer": a.ToPeer, "text": a.Text, "kind": firstNonempty(a.Kind, "notify")}
		optional(body, "fire_at", a.FireAt)
		optional(body, "cron", a.Cron)
		optional(body, "circle", a.Circle)
		result, err := h.mcpLocal(ctx, http.MethodPost, "/schedules", body)
		return stringValue(result, "schedule_id"), err
	}
}

func optional(body map[string]any, key, value string) {
	if value != "" {
		body[key] = value
	}
}
func required(name, value string) error {
	if value == "" {
		return fmt.Errorf("%s is required", name)
	}
	return nil
}

func requireFields(fields ...string) error {
	for index := 0; index+1 < len(fields); index += 2 {
		if err := required(fields[index], fields[index+1]); err != nil {
			return err
		}
	}
	return nil
}
func defaultInt(value, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}
func addQuery(q url.Values, key, value string) {
	if value != "" {
		q.Set(key, value)
	}
}
func withQuery(path string, q url.Values) string {
	if len(q) == 0 {
		return path
	}
	return path + "?" + q.Encode()
}
func structMap(value any) map[string]any {
	raw, _ := json.Marshal(value)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	for key, value := range out {
		switch value := value.(type) {
		case string:
			if value == "" {
				delete(out, key)
			}
		case nil:
			delete(out, key)
		}
	}
	return out
}
func stringValue(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	value, _ := m[key].(string)
	return value
}
func firstNonempty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

var _ = strconv.Itoa
