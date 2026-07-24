package service

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/repowire/repowire/daemon-go/proto"
)

func TestResolveCommandUsesConfiguredPath(t *testing.T) {
	dir := t.TempDir()
	command := filepath.Join(dir, "custom-agent")
	if err := os.WriteFile(command, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	svc := NewSpawnService(nil, nil, map[proto.AgentType]string{proto.AgentCodex: "custom-agent"}, []string{dir}).WithRuntimeConfig(nil, map[string]string{"PATH": dir})
	if _, err := svc.ResolveCommand(proto.AgentCodex, nil); err != nil {
		t.Fatalf("configured executable should resolve: %v", err)
	}
	quotedDir := filepath.Join(t.TempDir(), "with space")
	if err := os.MkdirAll(quotedDir, 0o700); err != nil {
		t.Fatal(err)
	}
	quotedCommand := filepath.Join(quotedDir, "custom agent")
	if err := os.WriteFile(quotedCommand, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	svc.commands[proto.AgentCodex] = "'" + quotedCommand + "' --flag"
	if _, err := svc.ResolveCommand(proto.AgentCodex, nil); err != nil {
		t.Fatalf("quoted executable path should resolve: %v", err)
	}
	svc.commands[proto.AgentCodex] = "missing-agent"
	_, err := svc.ResolveCommand(proto.AgentCodex, nil)
	var spawnErr *SpawnError
	if !errors.As(err, &spawnErr) || spawnErr.Status != 422 {
		t.Fatalf("missing executable: want 422 SpawnError, got %v", err)
	}
}
