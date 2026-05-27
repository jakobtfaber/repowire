# Spawning

## What it does

Spawning starts a new agent peer from a configured backend command and allowed project path.

## Where available

- CLI: `repowire peer new`
- CLI: `repowire peer restart`
- MCP: `spawn_peer`
- Dashboard and orchestrator flows that use the same spawn configuration

## Limits

Spawning is disabled until `daemon.spawn.allowed_paths` and backend commands are configured. Profiles append structured args to the configured backend command; Repowire does not hardcode provider model names.

Killing or restarting a tmux pane is only allowed when the daemon can prove Repowire spawned it. Externally attached peers and stale pane records are deregistered without touching tmux.

Dashboard spawn and backend controls use the same allowed paths, backend commands, and profile configuration as CLI and MCP surfaces.

## Related

- [Guide: spawn another peer](../guides/spawn-peer.md)
- [Configuration: spawn](../reference/configuration.md#daemonspawn)
- [CLI reference: peer](../reference/cli.md#repowire-peer)
- [MCP tools: spawn_peer](../reference/mcp-tools.md#spawn_peer)
