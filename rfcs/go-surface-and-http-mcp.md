# Go the full surface + HTTP MCP

Status: direction approved (2026-07-01). Phases tracked in beads:
repowire-53c (phase 1) → repowire-76o (phase 2), repowire-sfh (HTTP MCP);
repowire-jx8 (phase 4). Sequenced after `feat/daemon-go` lands.

## Why

The Go hub port (feat/daemon-go) proved the language fit: the daemon came out
structurally better than the Python original (typed PeerID/DisplayName, a
compiler-enforced peer↔state seam, real shutdown lifecycles). Two pressures
push the rest of the surface the same way:

- **Hooks fire as fresh subprocesses on every agent turn** (SessionStart /
  Stop / UserPromptSubmit). Each invocation pays the Python interpreter +
  import tax (~100–300 ms) versus ~5 ms for a static Go binary. This is the
  one place users feel the runtime on every single turn.
- **Distribution.** A single static binary removes uv/venv variance and the
  "hooks run from the installed package" reinstall foot-gun. During the
  transition, wheels keep wrapping the binary so `uv tool install repowire`
  continues to work (platform wheels already ship the Go hub via the hatch
  hook).

What stays as-is: the channel server and web dashboard are TypeScript and
remain so (transports are client-side; the daemon philosophy is unchanged).

## Scope reality

Remaining Python (~26k lines outside the daemon, plus a 42k-line pytest
suite):

| Surface | ~Lines | Notes |
|---|---|---|
| daemon remainder | ~10k | migrations, config loader, resume_safety, diagnostics — Go hub still depends on Python for the first two |
| cli.py + top-level (client, spawn, doctor, agent_backends) | ~16k | mechanical (click → cobra), largest chunk |
| hooks | ~4.3k | small surface, biggest felt win |
| mcp/server.py | ~1.9k | superseded by HTTP MCP below |
| relay server | ~1.4k | Go arguably better for the GKE service anyway |
| telegram + slack bots | ~1.6k | straightforward |

The pytest suite is the hidden majority of the cost: the Go hub port survived
seven review rounds *because* the Python suite was the oracle. Porting each
surface means porting (or wiring the Go implementation into) its oracle first.
`agent_backends.py` churns often — while it exists in both languages, every
backend tweak lands twice; phase 1/2 should be quick successive, not parallel
long-lived.

## Phases

1. **Daemon independence** (repowire-53c): config.yaml loader, state DB
   migrations, resume_safety in Go. Deletes the Python fallback from
   `repowire serve` entirely — the CLI stops pre-migrating and stops threading
   config as flags.
2. **Hooks** (repowire-76o): per-turn latency win. The ws-hook, session/stop/
   prompt/notification handlers, adapters. Go binary already installed with
   the wheel.
3. **HTTP MCP** (repowire-sfh, parallel-izable with 2): see below. Retires
   mcp/server.py instead of porting it.
4. **CLI + single-binary distribution** (repowire-jx8): cobra CLI, wheels wrap
   the binary during transition. Bots and relay server slot in anywhere.

## HTTP MCP: /mcp on the daemon + stdio identity shim

Move all MCP logic behind a streamable-HTTP `/mcp` endpoint on the (Go)
daemon. MCP-over-HTTP is JSON-RPC POSTs plus optional SSE — both already
served by the hub and carried by the relay tunnel unchanged.

**The identity constraint (the load-bearing design point).** The stdio MCP
server is not just transport: it is a per-session identity shim. Each agent
spawns its own MCP process, which inherits that agent's env and cwd — that is
what whoami/backend detection/session binding run on. A single shared local
HTTP endpoint erases this: the daemon sees N identical connections. In-protocol
inference does not recover it locally — `clientInfo` says "claude-code" for
every session, and MCP roots give a project path, but same-path peers (spawned
reviewers) are the canonical collision; path/cwd alone is not identity (that
is the registry's core philosophy). There is also no reliable way to inject a
per-session token into static MCP config headers.

**Resolution: keep a paper-thin stdio shim at the edge.** Spawned per-agent as
today, so it inherits env+cwd; it stamps identity headers
(`X-Repowire-Peer`, session id) and blindly proxies JSON-RPC to the daemon's
`/mcp`. ~100 lines; can be the hub binary with an `--mcp-stdio` flag. The
stdio hop survives *because it is the identity channel*, not for protocol
reasons. All tools, registration, and binding logic live daemon-side, once.

**Remote access composes later** (not in scope now): the same `/mcp` rides
the relay tunnel. Remote callers without shim headers register as
steering-peers (the @telegram/@dashboard class: no cwd, no pane,
connection-based liveness — the mesh already supports this as first-class).
Identity chain remote: bearer token → mesh/machine; `Mcp-Session-Id` → peer;
`clientInfo` → backend; roots (when offered) → project. Reconnects adopt by
identity tuple (token + clientInfo + root), the same move as the Go hub's
restart mapping adoption. What cannot be inferred remotely — pane, PID,
branch — maps onto the existing runtime-evidence ceiling: no destructive pane
proof means kill/restart refuses, which is correct for a peer whose runtime we
cannot see. Destructive tools (spawn_peer/kill_peer/schedule_*) require an
elevated token scope; the default remote scope is the steering set
(list_peers, ask, ack, notify, broadcast, review_queue). claude.ai custom
connectors additionally need OAuth 2.1 + dynamic client registration on the
relay — that is the bulk of any remote-phase effort and lives relay-side.
