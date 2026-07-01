package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// pane_runtime.go ports the small read/clear half of the hook-written pane
// runtime state (repowire/hooks/utils.py). The ws-hook writes a per-pane
// ws-hook-<pane>.meta.json under ~/.cache/repowire/logs/ naming the logical
// session that owns the pane; the daemon reads its peer_id as the third
// destructive-pane proof mode, and clears the files on a verified kill/restart
// or pane death. Writing these files stays a hook (client) concern — the Go hub
// only reads and clears.

// paneLogsDir mirrors utils.pane_logs_dir(): CACHE_DIR/logs where
// CACHE_DIR = ~/.cache/repowire.
func paneLogsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".cache", "repowire", "logs")
	}
	return filepath.Join(home, ".cache", "repowire", "logs")
}

// paneFileToken mirrors utils.get_pane_file(): strip %, /, \ ; "unknown" if empty.
func paneFileToken(paneID string) string {
	s := strings.NewReplacer("%", "", "/", "", "\\", "").Replace(paneID)
	if s == "" {
		return "unknown"
	}
	return s
}

func WSHookMetaPath(paneID string) string {
	return filepath.Join(paneLogsDir(), "ws-hook-"+paneFileToken(paneID)+".meta.json")
}

// ReadPaneRuntimeMetadata reads the ws-hook meta.json for a pane. Missing/invalid
// → empty map (best-effort, exactly like the Python reader). The legacy cwd
// fallback is not ported — only peer_id matters for the destructive proof.
func ReadPaneRuntimeMetadata(paneID string) map[string]any {
	if paneID == "" {
		return map[string]any{}
	}
	raw, err := os.ReadFile(WSHookMetaPath(paneID))
	if err != nil {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil || m == nil {
		return map[string]any{}
	}
	return m
}

// ClearPaneRuntimeState removes the transient pane-scoped hook files after a
// verified kill/restart or pane death (parity with utils.clear_pane_runtime_state).
// Best-effort: a missing file is not an error. ponytail: pending-query-cid files
// are hook-owned and not cleared here; the meta/pid/cwd trio is what gates the
// destructive proof and stale-pane reuse.
func ClearPaneRuntimeState(paneID string) {
	if paneID == "" {
		return
	}
	tok := paneFileToken(paneID)
	dir := paneLogsDir()
	for _, name := range []string{"ws-hook-" + tok + ".pid", "ws-hook-" + tok + ".meta.json", "ws-hook-" + tok + ".cwd"} {
		_ = os.Remove(filepath.Join(dir, name))
	}
}
