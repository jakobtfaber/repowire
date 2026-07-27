# Spawning

## What it is

Spawning starts or restarts agent peers from configured backend commands and allowed project paths. It is the daemon-controlled alternative to opening another terminal manually.

## When to use it

Use spawning when an orchestrator, dashboard, CLI command, or MCP tool should launch another peer in a known project folder. Use manual launch when you need ad hoc terminal control outside the configured allowed paths.

Use restart when the backend supports resume and the existing peer has a captured runtime session id. Restart is strict: Repowire pre-validates resume data before killing a live pane.

## Setup

Configure spawn commands and allowed paths in `~/.repowire/config.yaml`:

```yaml
daemon:
  spawn:
    allowed_paths:
      - ~/projects
    commands:
      claude-code: claude
      codex: codex
    env_path:
      - ~/.local/bin
      - ~/.nvm/versions/node/v23.9.0/bin
      - /opt/homebrew/bin
      - /usr/bin
      - /bin
```

Profiles can append structured args to configured backend commands. Repowire does not hardcode provider model names.

`env_path` is optional. When set, Repowire injects it as the spawned agent's `PATH`
before launching the backend command. This is useful for durable job workers
started by the daemon, because they should not depend on whichever PATH the tmux
server happened to inherit. When `env_path` is omitted, Repowire captures the
user's login-shell PATH and injects that as a fallback.

## Common workflows

Spawn a peer from CLI:

```bash
repowire peer new ~/projects/project-a
repowire peer new ~/projects/project-b --backend codex --profile fast
```

From an agent, use the `spawn_peer` MCP tool when the path is allowed.

Successful spawn calls for hook-backed agents return only after registration.
The response includes the canonical display name and peer id, so an orchestrator
can address the worker immediately. To inspect the registered peer:

```bash
repowire peer list
```

Codex may register only after its first interaction; spawn seed messages trigger
that first turn. If registration does not complete within 45 seconds, spawn
fails and removes the unregistered pane. When pane removal or durable cleanup
fails, Repowire preserves the remaining ownership proof and reports
`spawn_cleanup_failed` instead of claiming cleanup succeeded.

Restart a resumable peer:

```bash
repowire peer restart <peer>
```

Dashboard spawn and backend controls use the same spawn configuration as CLI and MCP surfaces.

## Commands and API

- CLI: `repowire peer new`, `repowire peer restart`.
- MCP: `spawn_peer`, `kill_peer`.
- HTTP: spawn and session-control routes exposed by the daemon.
- Dashboard: spawn dialog and backend/profile controls.

## Limits

- Spawning is disabled until `daemon.spawn.allowed_paths` and backend commands are configured.
- A path outside the allowed roots is rejected.
- Killing a tmux pane is allowed only when the daemon can prove the pane belongs to the target peer: Repowire spawn ownership, or live pane hook metadata whose `peer_id` matches the target.
- Externally attached peers without matching metadata cannot have their pane killed by Repowire.
- Restart does not fall back to a fresh spawn when resume data is missing or stale.

## Troubleshooting

- Spawn is refused: check `daemon.spawn.allowed_paths` and backend command configuration.
- Spawn reports `spawn_registration_timeout`: confirm the seed prompt reached
  the worker and that Codex fires `SessionStart`; the timed-out pane was removed.
- Spawn reports `spawn_cleanup_failed`: inspect the returned peer and pane;
  Repowire preserved the remaining ownership proof because cleanup could not
  be confirmed.
- Restart fails before killing the pane: inspect the session binding and backend resume support.
- Kill is refused for an external pane: rehook/link the pane or retire it manually; destructive pane control requires proof.

## See also

- [Configuration: spawn](../../reference/configuration.md#daemonspawn)
- [CLI reference](../../reference/cli.md#repowire-peer)
- [MCP tools](../../reference/mcp-tools.md#spawn_peer)
- [Peer identity lifecycle](../../concepts/peer-identity-lifecycle.md)
