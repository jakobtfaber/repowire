package hub

import (
	"encoding/json"
	"os"
	"testing"
)

func writePaneMeta(t *testing.T, paneID string, meta map[string]any) {
	t.Helper()
	if err := os.MkdirAll(paneLogsDir(), 0o755); err != nil {
		t.Fatalf("mkdir pane logs: %v", err)
	}
	raw, _ := json.Marshal(meta)
	if err := os.WriteFile(WSHookMetaPath(paneID), raw, 0o644); err != nil {
		t.Fatalf("write meta: %v", err)
	}
}

func TestReadPaneRuntimeMetadata(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if got := ReadPaneRuntimeMetadata("%9"); len(got) != 0 {
		t.Fatalf("absent meta should be empty, got %v", got)
	}
	writePaneMeta(t, "%9", map[string]any{"peer_id": "repow-x-1"})
	if got, _ := ReadPaneRuntimeMetadata("%9")["peer_id"].(string); got != "repow-x-1" {
		t.Fatalf("peer_id = %q, want repow-x-1", got)
	}
}
