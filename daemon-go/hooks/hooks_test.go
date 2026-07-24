package hooks

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestNormalizeBackendPayloads(t *testing.T) {
	p := Normalize(map[string]any{
		"hook_event_name": "AfterAgent",
		"session_id":      "s1",
		"final_response":  "done",
		"model":           map[string]any{"modelID": "gemini-3"},
	}, "gemini")
	if p.Event != "Stop" || p.SessionID != "s1" || p.ResponseText != "done" || p.Model != "gemini-3" {
		t.Fatalf("unexpected normalization: %+v", p)
	}
}

func TestLastTurnAndHandledCIDs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	transcript := strings.Join([]string{
		`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"question"}]}}`,
		`{"type":"assistant","uuid":"turn-1","message":{"role":"assistant","content":[{"type":"text","text":"answer"},{"type":"tool_use","name":"mcp__repowire__ack","input":{"correlation_id":"ask-1"}}]}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(transcript), 0o600); err != nil {
		t.Fatal(err)
	}
	user, assistant, turnID, calls := lastTurn(path)
	if user != "question" || assistant != "answer" || turnID != "turn-1" {
		t.Fatalf("unexpected turn: %q %q %q", user, assistant, turnID)
	}
	if !handledCIDs(calls)["ask-1"] {
		t.Fatalf("ack was not recognized: %+v", calls)
	}
}

func TestHandoffSummaryIsBounded(t *testing.T) {
	summary := handoffSummary("", strings.Repeat("word ", 400), "")
	if got := len(strings.Fields(summary)); got > 301 {
		t.Fatalf("handoff has %d words", got)
	}
}

func TestMCPIdentityReregistersAfterCachedCertificateFails(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("TMUX_PANE", "%999")
	t.Setenv("REPOWIRE_BACKEND", "codex")
	t.Setenv("REPOWIRE_PEER_ID", "")
	t.Setenv("REPOWIRE_CONFIG", filepath.Join(homeDir, "missing-config.yaml"))
	cwd := mustGetwd()
	hash := sha256.Sum256([]byte(cwd + "::codex"))
	hintPath := cachePath("spawn-hints", hex.EncodeToString(hash[:])[:16]+".json")
	if err := os.MkdirAll(filepath.Dir(hintPath), 0o700); err != nil {
		t.Fatal(err)
	}
	hint, _ := json.Marshal([]map[string]any{{"circle": "chosen", "role": "orchestrator", "ts": float64(time.Now().Unix())}})
	if err := os.WriteFile(hintPath, hint, 0o600); err != nil {
		t.Fatal(err)
	}

	oldCert := map[string]any{"nonce": "expired", "peer_id": "repow-old"}
	if err := writeMetadata("%999", map[string]any{
		"backend": "codex", "cwd": mustGetwd(), "peer_id": "repow-old",
		"agent_pid": os.Getppid(), "birth_certificate": oldCert,
	}); err != nil {
		t.Fatal(err)
	}

	registrations := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		switch r.URL.Path {
		case "/peers/identity/validate":
			var body struct {
				Certificate map[string]any `json:"birth_certificate"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.Certificate["nonce"] != "fresh" {
				http.Error(w, "expired", http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"peer": map[string]any{"peer_id": "repow-fresh", "display_name": "repowire-codex"}})
		case "/peers":
			registrations++
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["role"] != nil {
				t.Errorf("unsigned hint role reached registration: %v", body["role"])
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"peer_id": "repow-fresh", "display_name": "repowire-codex",
				"birth_certificate": map[string]any{"nonce": "fresh", "peer_id": "repow-fresh"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	port, _ := strconv.Atoi(strings.TrimPrefix(server.URL, "http://127.0.0.1:"))
	t.Setenv("REPOWIRE_DAEMON__HOST", "127.0.0.1")
	t.Setenv("REPOWIRE_DAEMON__PORT", strconv.Itoa(port))
	t.Setenv("REPOWIRE_DAEMON__AUTH_TOKEN", "test-token")

	identity, proof := MCPIdentityProof()
	if identity != "repow-fresh" || proof != "fresh" || registrations != 1 {
		t.Fatalf("identity=%q proof=%q registrations=%d", identity, proof, registrations)
	}
}
