package hooks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
