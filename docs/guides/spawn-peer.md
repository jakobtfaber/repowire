# Spawn Another Peer

## Goal

Start another agent session through Repowire instead of opening a terminal manually.

## Before you start

Configure `daemon.spawn.commands` and `daemon.spawn.allowed_paths` in `~/.repowire/config.yaml`.

## Steps

```bash
repowire peer new ~/projects/project-a
repowire peer new ~/projects/project-b --backend codex --profile fast
```

From an agent, use the `spawn_peer` MCP tool when the configured path is allowed.

## Verify

Run:

```bash
repowire peer list
```

The new peer should appear after its runtime starts and registers.

## Related

- [Capabilities: spawning](../capabilities/spawning.md)
- [Configuration: spawn](../reference/configuration.md#daemonspawn)
- [MCP tools: spawn_peer](../reference/mcp-tools.md#spawn_peer)
