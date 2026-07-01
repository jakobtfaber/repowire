# daemon-go

Go port of the repowire daemon hub. Same WebSocket/HTTP wire protocol as the
Python daemon, reads the same `~/.repowire/state.db` (schema v12). Existing
clients — hooks, MCP server, Telegram/Slack bots — connect unchanged; only the
hub is Go.

## Why

Peer connect/disconnect bugs were logic-and-typing bugs. Go makes the worst
class uncompilable: `PeerID` and `DisplayName` are distinct types, so a
display-name lookup where a peer_id is required is a compile error, not a 2am
misroute. Lifecycle is a typed FSM (`peer/fsm.go`) with an exhaustive
transition function; an unhandled state/event pair fails loud.

## Layout

- `proto/` — wire types, `PeerID`/`DisplayName` (distinct), `PeerStatus` enum
- `state/` — SQLite store over the existing schema (pure-Go `modernc.org/sqlite`)
- `peer/` — `Registry`, lifecycle FSM, reconciliation (redeliver, demote, evict)
- `hub/` — WS server, router/transport, delivery, ask-tracker, HTTP routes
- `main.go` — wires `state.Store` → `peer.Registry` → `hub`

## Run

```bash
go build -o repowire-hub-go .
./repowire-hub-go -addr 127.0.0.1:8377 -db ~/.repowire/state.db
go test ./...
```

`repowire serve` launches this binary by default. Override:
- `REPOWIRE_DAEMON=python` — use the Python daemon instead
- `REPOWIRE_HUB_BIN=/path` — explicit binary path (else PATH `repowire-hub-go`, then dev build here)
- Relay is supported: the Go hub dials the relay itself (`relay/` package, `-relay-url`/`-relay-api-key`, threaded from config by `serve`). Only `REPOWIRE_DAEMON=python` or a missing binary falls back.

## Verified

13/13 live scenarios over HTTP+WS against a copy of a real state DB: register,
list, ws connect, ask delivery + ack, notify, broadcast, ws disconnect →
offline, and the session-closed evidence gate (spares peers with a live tmux
pane, offlines only those without evidence).

## Deferred (still Python, or not yet ported)

Clients/separate deployments — intentionally NOT daemon code: the hosted relay
SERVER (`relay/server.py`, GKE), Telegram/Slack bots, channel/ACP MCP transport,
the `hooks/` ws-hook supervisor, legacy `sessions.json` import. (The relay
CLIENT is ported — see `relay/`.)

Not yet ported in the hub: full config (yaml) loading (db/addr/auth wired via
flags+env; spawn allowlist/relay read minimally), the ACP subprocess transport
(dormant stub), `/ask-many` blocking variants, packaging the binary into the
wheel so `uv tool install` ships it (resolver falls back to Python until then).
