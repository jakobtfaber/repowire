# Contributing to Repowire

Thanks for wanting to contribute! Here's everything you need to get up and running.

## Setting Up the Dev Environment

You'll need Python 3.11+, [uv](https://docs.astral.sh/uv/getting-started/installation/), and [bun](https://bun.sh/) if you're touching the channel server.
```bash
# Clone the repo
git clone https://github.com/prassanna-ravishankar/repowire.git
cd repowire

# Install dev dependencies (pytest, ruff, ty, httpx-ws)
uv sync --extra dev

# Install repowire globally — hooks run from the installed package, not source
uv tool install --force --reinstall .
```

If you're working on the channel server:
```bash
cd repowire/channel && bun install
```

## Running Tests and Linting

Before pushing anything, make sure these all pass:
```bash
pytest                        # 222 tests
ruff check repowire/          # lint
uv run ty check repowire/     # type check
```

CI runs all three on every PR, so it's easier to catch issues locally first.

## How Hooks Work

This is the most common gotcha for new contributors: hooks run from the **installed package**, not directly from your source files. So after any code change, you need to reinstall before your changes will actually take effect:
```bash
uv tool install --force --reinstall .
```

If your changes aren't showing up, this is almost always why.

## PR Workflow

Fork the repo, create a branch, make your changes, and open a PR against `main`. Try to keep PRs focused on one thing — it makes review a lot faster.
```bash
git checkout -b your-branch-name
# make your changes
git add .
git commit -m "short description of what and why"
git push origin your-branch-name
```

## Code Style

Repowire uses [ruff](https://docs.astral.sh/ruff/) with a line length of 100. The full config is in `pyproject.toml`. You can auto-fix most issues with:
```bash
ruff check repowire/ --fix
```

## Where to Find Things

`CLAUDE.md` has the full architecture overview — worth reading before diving in. Here's a quick map of the main areas:

| Module | What it does |
|---|---|
| `daemon/` | Central routing hub — peer registry, message router, query tracker, HTTP routes |
| `hooks/` | Default Claude Code transport — session, stop, prompt, notification handlers |
| `channel/server.ts` | Experimental MCP stdio transport (requires bun) |
| `mcp/server.py` | MCP tools: `list_peers`, `ask_peer`, `notify_peer`, etc. |
| `relay/server.py` | Hosted relay at repowire.io — WS bridge and HTTP tunnel |
| `telegram/bot.py` | Telegram bot peer for mobile mesh control |
| `slack/bot.py` | Slack bot peer via Socket Mode |
| `web/` | Next.js dashboard — build with `repowire build-ui` |

One thing worth knowing before you start: repowire follows a **lazy repair** philosophy. Nothing polls. Work is deferred until needed and piggy-backed on incoming requests. So when contributing, avoid adding polling loops, periodic timers, or eager disk writes — it goes against the grain of how the whole system is designed.

## Questions?

Open an issue or start a discussion. Happy to help you get your first PR in.
