# Install

Repowire runs on macOS and Linux with tmux. Platform wheels contain the native
Go substrate; the Python package wrapper remains for installation and the
separate Telegram/Slack/hosted-relay clients.

## Recommended

```bash
curl -sSf https://raw.githubusercontent.com/prassanna-ravishankar/repowire/main/install.sh | sh
```

The installer detects `uv`, `pipx`, and `pip` in that order, installs Repowire with the first available package manager, then drops you into [setup](setup.md).

## Alternatives

```bash
uv tool install repowire
pipx install repowire
pip install repowire
```

Use `uv` for the fastest isolated install. Use `pipx` if you want isolation without uv. Use plain `pip` only inside a virtualenv you control.

## What gets installed

The package ships:

- The native `repowire` CLI and daemon (HTTP + WebSocket on `127.0.0.1:8377`).
- The daemon-owned Streamable HTTP MCP implementation and per-agent stdio identity shim.
- Native hook/ws-hook handlers for every detected agent runtime.
- Embedded OpenCode, Pi, Claude channel, and orchestrator workspace assets.
- The Next.js dashboard, pre-built and served from the daemon at `/dashboard`.

Nothing runs yet. Run [setup](setup.md) to wire the hooks for your agents.

Repowire does not install third-party agent skills. If you want reusable `SKILL.md` packages, use a skills installer such as [Vercel Labs `skills`](https://github.com/vercel-labs/skills) alongside Repowire.
