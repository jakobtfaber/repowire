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

Killing a tmux pane is allowed when the daemon can prove the pane belongs to the target peer: Repowire spawn ownership, or live pane hook metadata whose `peer_id` matches the target. Externally attached peers without matching metadata are refused and left registered.

Restart is strict backend resume. Repowire validates the captured runtime session id before killing any live pane; stale or missing resume data returns an error instead of falling back to a fresh spawn.

Dashboard spawn and backend controls use the same allowed paths, backend commands, and profile configuration as CLI and MCP surfaces.

## Related

- [Guide: spawn another peer](../guides/spawn-peer.md)
- [Configuration: spawn](../reference/configuration.md#daemonspawn)
- [CLI reference: peer](../reference/cli.md#repowire-peer)
- [MCP tools: spawn_peer](../reference/mcp-tools.md#spawn_peer)
