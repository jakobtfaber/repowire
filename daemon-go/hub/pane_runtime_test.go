package hub

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/repowire/repowire/daemon-go/proto"
)

func writePaneMeta(t *testing.T, paneID string, meta map[string]any) {
	t.Helper()
	if err := os.MkdirAll(paneLogsDir(), 0o755); err != nil {
		t.Fatalf("mkdir pane logs: %v", err)
	}
	raw, _ := json.Marshal(meta)
	if err := os.WriteFile(wsHookMetaPath(paneID), raw, 0o644); err != nil {
		t.Fatalf("write meta: %v", err)
	}
}

func TestReadPaneRuntimeMetadata(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if got := readPaneRuntimeMetadata("%9"); len(got) != 0 {
		t.Fatalf("absent meta should be empty, got %v", got)
	}
	writePaneMeta(t, "%9", map[string]any{"peer_id": "repow-x-1"})
	if got, _ := readPaneRuntimeMetadata("%9")["peer_id"].(string); got != "repow-x-1" {
		t.Fatalf("peer_id = %q, want repow-x-1", got)
	}
}

// TestDestructivePaneProof_VerifiedByPaneMetadata proves the third proof mode:
// a live pane whose ws-hook meta.json names THIS peer_id authorizes destructive
// control; a mismatching/absent file does not (path match alone is never proof).
func TestDestructivePaneProof_VerifiedByPaneMetadata(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("REPOWIRE_CONFIG_DIR", t.TempDir())

	pane := "%77"
	tmux := &fakeTmux{killOK: true, evidence: map[string]*TmuxPaneEvidence{pane: {TmuxSession: "sess"}}}
	own := NewFileOwnership("test-host", tmux.ProbePane)
	svc := NewSpawnService(tmux, own,
		map[proto.AgentType]string{proto.AgentClaudeCode: "claude"}, []string{t.TempDir()})
	h := &Hub{}
	h.WithSpawn(svc, nil, NewAskTracker(0), "test-host")

	id := proto.PeerID("repow-ops-abc123")
	p := &proto.Peer{PeerID: id, PaneID: &pane}

	writePaneMeta(t, pane, map[string]any{"peer_id": string(id)})
	if proof := h.destructivePaneProof(p); !proof.ok || proof.mode != "verified_pane_metadata" {
		t.Fatalf("matching pane metadata must prove ownership: ok=%v mode=%q err=%q",
			proof.ok, proof.mode, proof.errCode)
	}

	writePaneMeta(t, pane, map[string]any{"peer_id": "repow-ops-someoneelse"})
	if proof := h.destructivePaneProof(p); proof.ok {
		t.Fatalf("mismatched pane metadata must NOT prove ownership")
	}
}
