package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/repowire/repowire/daemon-go/proto"
	"github.com/repowire/repowire/daemon-go/state"
)

// postJSON is a small helper: marshal body, POST it to the mux, return the
// recorder.
func postLifecycleJSON(t *testing.T, mux *http.ServeMux, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req := httptest.NewRequest(http.MethodPost, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// TestRegisterPeerEndpoint is the primary handler test: POST /peers registers a
// peer through the registry FSM and returns the canonical peer_id + assigned
// display_name in the Python wire shape. A follow-up offline + unregister
// exercises the rest of the lifecycle group, including the 404 path.
func TestRegisterPeerEndpoint(t *testing.T) {
	h := newTestHub(t)
	mux := http.NewServeMux()
	h.Routes(mux)

	path := "/work/myproj"
	rec := postLifecycleJSON(t, mux, "/peers", RegisterPeerRequest{
		Name:    "myproj-claude-code",
		Path:    &path,
		Backend: proto.AgentClaudeCode,
		Circle:  strptr("default"),
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /peers: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var resp RegisterResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode RegisterResponse: %v", err)
	}
	if !resp.OK {
		t.Fatalf("RegisterResponse.ok must be true: %+v", resp)
	}
	if resp.PeerID == "" {
		t.Fatalf("a canonical peer_id must be minted, got empty")
	}
	if resp.DisplayName != "myproj-claude-code" {
		t.Fatalf("display_name: want myproj-claude-code, got %q", resp.DisplayName)
	}
	if !resp.PaneAssigned {
		t.Fatalf("pane_assigned must be true when no pane was requested")
	}

	// The peer must now resolve in the registry, ONLINE, keyed by its peer_id.
	p, ok := h.reg.GetPeer(proto.PeerID(resp.PeerID))
	if !ok {
		t.Fatalf("registered peer must resolve by peer_id %q", resp.PeerID)
	}
	if p.Status != proto.StatusOnline {
		t.Fatalf("freshly registered (no pane+runtime) peer must be ONLINE, got %s", p.Status)
	}

	// POST /peers/{name}/offline → 200 with the OfflineResponse shape.
	offRec := postLifecycleJSON(t, mux, "/peers/"+resp.DisplayName+"/offline", OfflineRequest{})
	if offRec.Code != http.StatusOK {
		t.Fatalf("offline: want 200, got %d (%s)", offRec.Code, offRec.Body.String())
	}
	var off OfflineResponse
	if err := json.Unmarshal(offRec.Body.Bytes(), &off); err != nil {
		t.Fatalf("decode OfflineResponse: %v", err)
	}
	if !off.OK {
		t.Fatalf("OfflineResponse.ok must be true")
	}
	if p2, _ := h.reg.GetPeer(proto.PeerID(resp.PeerID)); p2.Status != proto.StatusOffline {
		t.Fatalf("peer must be OFFLINE after /offline, got %s", p2.Status)
	}

	// POST /peer/unregister of a live name → 200; a second call → 404.
	unRec := postLifecycleJSON(t, mux, "/peer/unregister", UnregisterPeerRequest{Name: resp.DisplayName})
	if unRec.Code != http.StatusOK {
		t.Fatalf("unregister: want 200, got %d (%s)", unRec.Code, unRec.Body.String())
	}
	if _, ok := h.reg.GetPeer(proto.PeerID(resp.PeerID)); ok {
		t.Fatalf("peer must be gone from the registry after unregister")
	}
	gone := postLifecycleJSON(t, mux, "/peer/unregister", UnregisterPeerRequest{Name: resp.DisplayName})
	if gone.Code != http.StatusNotFound {
		t.Fatalf("unregister of an unknown peer: want 404, got %d (%s)", gone.Code, gone.Body.String())
	}
}

// TestSetDescriptionUnknownPeer404 covers the description endpoint's 404 path:
// an unknown name must not be papered over.
func TestSetDescriptionUnknownPeer404(t *testing.T) {
	h := newTestHub(t)
	mux := http.NewServeMux()
	h.Routes(mux)

	rec := postLifecycleJSON(t, mux, "/peers/nope-claude-code/description", SetDescriptionRequest{
		Description: "reviewing PR #1",
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("description for unknown peer: want 404, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestSetCircleMovesPeer verifies POST /peers/circle moves a peer between circles
// (peer + durable mapping stay in sync via SetCircleByName).
func TestSetCircleMovesPeer(t *testing.T) {
	h := newTestHub(t)
	mux := http.NewServeMux()
	h.Routes(mux)

	path := "/work/proj"
	rec := postLifecycleJSON(t, mux, "/peers", RegisterPeerRequest{
		Name:   "proj-claude-code",
		Path:   &path,
		Circle: strptr("alpha"),
	})
	var resp RegisterResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)

	cRec := postLifecycleJSON(t, mux, "/peers/circle", SetCircleRequest{
		PeerName: resp.DisplayName,
		Circle:   "beta",
	})
	if cRec.Code != http.StatusOK {
		t.Fatalf("set circle: want 200, got %d (%s)", cRec.Code, cRec.Body.String())
	}
	p, ok := h.reg.GetPeer(proto.PeerID(resp.PeerID))
	if !ok || p.Circle != "beta" {
		t.Fatalf("peer must have moved to circle beta, got %+v ok=%v", p, ok)
	}
}

func TestRegisterPeerPersistsResumeCapability(t *testing.T) {
	store, err := state.NewStore(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	h := newTestHub(t)
	h.store = store
	mux := http.NewServeMux()
	h.Routes(mux)

	path := t.TempDir()
	runtimeID := "runtime-123"
	rec := postLifecycleJSON(t, mux, "/peers", RegisterPeerRequest{
		Name:    "codex-1",
		Path:    &path,
		Backend: proto.AgentCodex,
		Circle:  strptr("default"),
		Metadata: map[string]any{
			"runtime_session_id": runtimeID,
		},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /peers: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}

	backend := string(proto.AgentCodex)
	binding, err := store.GetByRuntimeSession(context.Background(), runtimeID, &backend, &path)
	if err != nil {
		t.Fatalf("GetByRuntimeSession: %v", err)
	}
	if binding == nil {
		t.Fatal("expected session binding for registered peer")
	}
	if binding.ResumeCapability["strategy"] != "codex_resume" {
		t.Fatalf("resume_capability = %v, want codex_resume strategy", binding.ResumeCapability)
	}
	if binding.ResumeCapability["supported"] != true {
		t.Fatalf("resume_capability.supported = %v, want true", binding.ResumeCapability["supported"])
	}
}

func strptr(s string) *string { return &s }
