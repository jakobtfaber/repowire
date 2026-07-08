package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMissingConfigUsesDefaults(t *testing.T) {
	t.Setenv("REPOWIRE_CONFIG", filepath.Join(t.TempDir(), "missing.yaml"))

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load missing: %v", err)
	}
	if cfg.Daemon.Host != defaultHost || cfg.Daemon.Port != defaultPort {
		t.Fatalf("daemon addr = %s:%d, want defaults", cfg.Daemon.Host, cfg.Daemon.Port)
	}
	if !cfg.Daemon.MCPHTTP.RequireAuth {
		t.Fatalf("mcp_http.require_auth default must stay true")
	}
	if cfg.Relay.URL != defaultRelayURL {
		t.Fatalf("relay url = %q, want %q", cfg.Relay.URL, defaultRelayURL)
	}
}

func TestLoadConfigYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	t.Setenv("REPOWIRE_CONFIG", path)
	if err := os.WriteFile(path, []byte(`
daemon:
  host: 0.0.0.0
  port: 9999
  auth_token: secret
  spawn:
    commands:
      claude-code: claude
      codex: codex --dangerously-bypass-approvals-and-sandbox
    allowed_paths:
      - /tmp/work
  mcp_http:
    enabled: true
relay:
  enabled: true
  url: wss://example.test
  api_key: key
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Daemon.Host != "0.0.0.0" || cfg.Daemon.Port != 9999 || cfg.Daemon.AuthToken != "secret" {
		t.Fatalf("daemon config not loaded: %+v", cfg.Daemon)
	}
	if cfg.Daemon.Spawn.Commands["codex"] == "" || len(cfg.Daemon.Spawn.AllowedPaths) != 1 {
		t.Fatalf("spawn config not loaded: %+v", cfg.Daemon.Spawn)
	}
	if !cfg.Daemon.MCPHTTP.Enabled || !cfg.Daemon.MCPHTTP.RequireAuth {
		t.Fatalf("mcp_http defaults/overrides wrong: %+v", cfg.Daemon.MCPHTTP)
	}
	if !cfg.Relay.Enabled || cfg.Relay.URL != "wss://example.test" || cfg.Relay.APIKey != "key" {
		t.Fatalf("relay config not loaded: %+v", cfg.Relay)
	}
}

func TestLoadEnvOverridesYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	t.Setenv("REPOWIRE_CONFIG", path)
	t.Setenv("REPOWIRE_DAEMON__PORT", "8888")
	t.Setenv("REPOWIRE_AUTH_TOKEN", "env-token")
	t.Setenv("REPOWIRE_SPAWN_COMMANDS", `{"codex":"codex"}`)
	t.Setenv("REPOWIRE_SPAWN_ALLOWED_PATHS", "/a,/b")
	if err := os.WriteFile(path, []byte(`
daemon:
  port: 9999
  auth_token: file-token
  spawn:
    commands:
      claude-code: claude
    allowed_paths:
      - /tmp/work
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Daemon.Port != 8888 || cfg.Daemon.AuthToken != "env-token" {
		t.Fatalf("env override failed: %+v", cfg.Daemon)
	}
	if got := cfg.Daemon.Spawn.CommandsJSON(); got != `{"codex":"codex"}` {
		t.Fatalf("commands json = %s", got)
	}
	if len(cfg.Daemon.Spawn.AllowedPaths) != 2 || cfg.Daemon.Spawn.AllowedPaths[1] != "/b" {
		t.Fatalf("allowed paths = %#v", cfg.Daemon.Spawn.AllowedPaths)
	}
}
