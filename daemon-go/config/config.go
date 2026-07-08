package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	defaultHost     = "127.0.0.1"
	defaultPort     = 8377
	defaultRelayURL = "wss://repowire.io"
)

type Config struct {
	Daemon DaemonConfig `yaml:"daemon"`
	Relay  RelayConfig  `yaml:"relay"`
}

type DaemonConfig struct {
	Host      string        `yaml:"host"`
	Port      int           `yaml:"port"`
	AuthToken string        `yaml:"auth_token"`
	Spawn     SpawnConfig   `yaml:"spawn"`
	MCPHTTP   MCPHTTPConfig `yaml:"mcp_http"`
}

type SpawnConfig struct {
	Commands     map[string]string `yaml:"commands"`
	AllowedPaths []string          `yaml:"allowed_paths"`
}

type MCPHTTPConfig struct {
	Enabled                       bool   `yaml:"enabled"`
	Bind                          string `yaml:"bind"`
	RequireAuth                   bool   `yaml:"require_auth"`
	ExposeViaRelay                bool   `yaml:"expose_via_relay"`
	AllowUnauthenticatedLocalhost bool   `yaml:"allow_unauthenticated_localhost"`
	AllowDangerousTools           bool   `yaml:"allow_dangerous_tools"`
}

type RelayConfig struct {
	Enabled bool   `yaml:"enabled"`
	URL     string `yaml:"url"`
	APIKey  string `yaml:"api_key"`
}

func Defaults() Config {
	return Config{
		Daemon: DaemonConfig{
			Host: defaultHost,
			Port: defaultPort,
			Spawn: SpawnConfig{
				Commands: map[string]string{},
			},
			MCPHTTP: MCPHTTPConfig{
				Bind:        "localhost-only",
				RequireAuth: true,
			},
		},
		Relay: RelayConfig{URL: defaultRelayURL},
	}
}

func Load() (Config, error) {
	cfg := Defaults()
	path := Path()
	if b, err := os.ReadFile(path); err == nil {
		if err := yaml.Unmarshal(b, &cfg); err != nil {
			return cfg, fmt.Errorf("parse %s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return cfg, fmt.Errorf("read %s: %w", path, err)
	}
	applyEnv(&cfg)
	normalize(&cfg)
	return cfg, nil
}

func Path() string {
	if p := os.Getenv("REPOWIRE_CONFIG"); p != "" {
		return p
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".repowire", "config.yaml")
	}
	return filepath.Join(".repowire", "config.yaml")
}

func (s SpawnConfig) CommandsJSON() string {
	if len(s.Commands) == 0 {
		return ""
	}
	b, _ := json.Marshal(s.Commands)
	return string(b)
}

func applyEnv(cfg *Config) {
	if v := firstEnv("REPOWIRE_DAEMON__HOST", "REPOWIRE_DAEMON_HOST"); v != "" {
		cfg.Daemon.Host = v
	}
	if v := firstEnv("REPOWIRE_DAEMON__PORT", "REPOWIRE_DAEMON_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Daemon.Port = n
		}
	}
	if v := firstEnv("REPOWIRE_DAEMON__AUTH_TOKEN", "REPOWIRE_AUTH_TOKEN"); v != "" {
		cfg.Daemon.AuthToken = v
	}
	if v := os.Getenv("REPOWIRE_SPAWN_COMMANDS"); v != "" {
		var commands map[string]string
		if json.Unmarshal([]byte(v), &commands) == nil {
			cfg.Daemon.Spawn.Commands = commands
		}
	}
	if v := os.Getenv("REPOWIRE_SPAWN_ALLOWED_PATHS"); v != "" {
		cfg.Daemon.Spawn.AllowedPaths = splitCSV(v)
	}
	if v := os.Getenv("REPOWIRE_RELAY_URL"); v != "" {
		cfg.Relay.URL = v
	}
	if v := os.Getenv("REPOWIRE_RELAY_API_KEY"); v != "" {
		cfg.Relay.APIKey = v
	}
}

func normalize(cfg *Config) {
	if cfg.Daemon.Host == "" {
		cfg.Daemon.Host = defaultHost
	}
	if cfg.Daemon.Port == 0 {
		cfg.Daemon.Port = defaultPort
	}
	if cfg.Daemon.Spawn.Commands == nil {
		cfg.Daemon.Spawn.Commands = map[string]string{}
	}
	if cfg.Daemon.MCPHTTP.Bind == "" {
		cfg.Daemon.MCPHTTP.Bind = "localhost-only"
	}
	if cfg.Relay.URL == "" {
		cfg.Relay.URL = defaultRelayURL
	}
}

func firstEnv(keys ...string) string {
	for _, key := range keys {
		if v := os.Getenv(key); v != "" {
			return v
		}
	}
	return ""
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
