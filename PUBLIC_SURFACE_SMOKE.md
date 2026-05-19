# Public Surface Smoke - v0.13.x ACP + UI Train

Date: 2026-05-19  
Branch: `test/public-surface-smoke`  
Version under test: `0.13.28`

## Scope

Smoked public entry points only: real `repowire serve` daemon processes, HTTP routes, WebSocket `/ws`, MCP stdio tool call, and an ACP subprocess fixture. No internal route helpers or direct registry calls were used for pass/fail checks. The `/ask` ACP check passed on this clean main-derived checkout at `0.13.28`; the known broken symptom for #206 was not present here.

Isolation used:
- Main daemon: `HOME=$(mktemp -d)`, port `19377`, `PYTHONPATH=.`, `PATH=/usr/bin:/bin:/usr/sbin:/sbin`.
- Missing-ACP daemon: separate temp `HOME`, port `19378`, separate venv without `agent-client-protocol`.
- `tmux` was intentionally excluded from `PATH` so daemon startup could not mutate live tmux hooks.

## Commands Run

```bash
uv run python - <<'PY'
import repowire
PY
```

Result: failed before smoke setup because editable build requires `web/out`, which is absent in this checkout.

```bash
uv pip install --python .venv/bin/python \
  'mcp>=1.0.0' 'fastapi>=0.109.0' 'uvicorn[standard]>=0.27.0' \
  'pydantic>=2.5.0' 'pydantic-settings>=2.1.0' 'pyyaml>=6.0.1' \
  'rich>=13.7.0' 'click>=8.1.7' 'httpx>=0.26.0' \
  'libtmux>=0.37.0' 'websockets>=13.0' \
  'agent-client-protocol>=0.10.0'
```

Then added minimal `repowire-0.13.28.dist-info` in `.venv` only so `importlib.metadata.version("repowire")` works with `PYTHONPATH=.`.

```bash
env HOME="$tmp" PYTHONPATH=. PATH="/usr/bin:/bin:/usr/sbin:/sbin" \
  .venv/bin/python -m repowire.cli serve --host 127.0.0.1 --port 19377
```

Second clean-runtime missing-SDK daemon:

```bash
uv venv /tmp/repowire-noacp-venv
uv pip install --python /tmp/repowire-noacp-venv/bin/python \
  'mcp>=1.0.0' 'fastapi>=0.109.0' 'uvicorn[standard]>=0.27.0' \
  'pydantic>=2.5.0' 'pydantic-settings>=2.1.0' 'pyyaml>=6.0.1' \
  'rich>=13.7.0' 'click>=8.1.7' 'httpx>=0.26.0' \
  'libtmux>=0.37.0' 'websockets>=13.0'

env HOME="$tmp_noacp" PYTHONPATH=. PATH="/usr/bin:/bin:/usr/sbin:/sbin" \
  /tmp/repowire-noacp-venv/bin/python -m repowire.cli serve \
  --host 127.0.0.1 --port 19378
```

Both daemons were stopped with:

```bash
POST http://127.0.0.1:{19377,19378}/shutdown
```

## Checklist

| Surface | Result | Notes |
|---|---:|---|
| `GET /health` | PASS | Returned `{"status":"ok","version":"0.13.28","relay_mode":false}`. |
| WebSocket `/ws` connect | PASS | Real WS peers connected and received daemon-assigned names. |
| HTTP `/notify` to live WS peer | PASS | Returned `200 {"ok":true,"status":"sent"}` and recipient WS received `type=notify`. |
| HTTP `/notify` to ACP-marked peer | NOT RUN | ACP notify was not part of the validated path. Current architecture notes say ACP phase-3 routes `ask` only and leaves notify on the WS path, so ACP-marked peers without WS presence are still expected to fail `/notify` unless a later patch changes that. Treat as untested/broken, not passed. |
| HTTP `/ask` WS path | PASS | Returned `ask-*`; recipient WS received `type=ask` with text plus ack instructions. |
| HTTP `/ack` reply path | PASS | Ask sender WS received framed `[ack #...]` notification. |
| Register ACP-marked peer through `POST /peers` | PASS | Public peer registration accepted `metadata.acp`. |
| HTTP `/ask` to ACP-marked peer | PASS | Returned `200`, no `503`; asker WS received `[echo] public acp ping` from ACP echo subprocess. |
| MCP `ask` | PASS | MCP stdio `ask` returned `ask-c45184c3`; target WS received `type=ask`. Tested same-circle target. |
| ACP missing SDK clean error | PASS | Clean venv without `acp` returned `/ask` `200` and delivered an ACP error ack with install hint. |
| `POST /events/chat_delta` + `GET /events` | PASS | Event appears as top-level `type=chat_turn_delta` fields in event buffer. |
| Late `chat_turn_delta` after final `chat_turn` | PASS | Late delta returned `200` and was dropped from `/events`. |
| `GET /peers/{name}/transcript` for Codex peer | PASS | Returned `200 {"turns":[],"next_before":null}`. |
| `GET /peers/no-such/transcript` | PASS | Returned `404`. |
| Backend switcher safe guard | PASS | With spawn disabled, local HTTP-registered peer returned `422 command_unavailable` before kill/spawn. |

## #206 Status

Not reproducible on this clean main-derived checkout / branch.

Public reproduction attempted:

1. Start isolated daemon with:
   ```yaml
   experiments:
     acp_broker_client: true
   ```
2. Connect asker over real WebSocket `/ws`.
3. Register answerer via public `POST /peers` with:
   ```json
   {
     "backend": "codex",
     "metadata": {
       "acp": {
         "command": ".venv/bin/python",
         "args": ["tests/fixtures/acp_echo_agent.py"],
         "cwd": "/tmp/..."
       }
     }
   }
   ```
4. Post public HTTP ask:
   ```bash
   curl -sS -X POST http://127.0.0.1:19377/ask \
     -H 'content-type: application/json' \
     -d '{"from_peer":"<asker_peer_id>","to_peer":"acp-codex","text":"public acp ping","circle":"smoke"}'
   ```

Observed on this branch:

```json
{"correlation_id":"ask-161dcf49","error":null}
```

The asker WebSocket received:

```text
[ack #ask-161dcf49 from @acp-codex] [echo] public acp ping
```

Expected broken-branch symptom for #206 would be `503 Peer acp-codex has no live connection` before ACP dispatch. That did not occur.

## Findings

### Suspected issue: editable/package install blocked by missing `web/out`

`uv run ...` failed while building editable `repowire`:

```text
FileNotFoundError: Forced include not found:
/Users/prass/development/projects/repowire.public-smoke/web/out
```

Impact: public local install/smoke commands (`uv run`, likely `uv sync`, `uv tool install .`) fail from a clean checkout unless the dashboard export exists first. This also created local `uv.lock` churn (`repowire` lock version changed from `0.13.25` to `0.13.28`); that churn was restored before this report.

Recommendation: patch. Either commit/build `web/out` before packaging, remove `web/out` from forced includes for editable/dev builds, or gate package-data inclusion so source checkouts can install before `repowire build-ui`.

### Risk: daemon startup installs tmux hooks even under temp `HOME`

The public `repowire serve` startup path calls `install_hooks(cfg.daemon.host, cfg.daemon.port)` when `tmux` is available. Because tmux hooks are server-global, a supposedly isolated daemon started with temp `HOME` and alternate port can still rewrite the user's live tmux hooks to the smoke daemon port.

I avoided executing that path by excluding `tmux` from `PATH`.

Recommendation: patch or small refactor. Add an explicit config/env/CLI guard to disable tmux hook installation during daemon startup, or move hook installation out of `serve` startup and keep it in `repowire setup`.

## Patch vs Refactor Recommendation

- #206: no patch needed on this clean main-derived checkout / branch based on public-surface smoke. If another branch still has the old ordering, the cheap route-dispatch reorder is the right patch before tagging.
- ACP `/notify`: not validated as working. If product intent is for ACP peers to receive notify/broadcast without WS presence, that is not covered by the current phase-3 ask-only path and should be tracked separately.
- Packaging `web/out`: patch before release tagging; it blocks basic public install/dev smoke from clean checkout.
- tmux hook side effect on `serve`: patch if smoke/release workflows will run alternate daemons on developer machines; otherwise schedule as small v0.14 hygiene.
- PeerTransport ACP architecture: keep as v0.14 refactor. The current branch's public `/ask` ACP path works end-to-end for the happy path and missing-SDK error path.
