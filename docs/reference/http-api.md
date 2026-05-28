# HTTP API

The daemon exposes HTTP routes for the dashboard, hooks, CLI helpers, and client libraries. The stable public client surface is the CLI, MCP tools, and Python client; raw HTTP routes may move faster.

## Primary route groups

- `/health` and status routes for daemon checks.
- `/peers` for peer registration, listing, lookup, and lifecycle operations.
- `/ask`, `/ack`, and `/asks/pending` for ask lifecycle.
- `/messages` and WebSocket routes for live delivery.
- `/schedules` for one-shot and recurring scheduled messages.
- `/jobs` / work routes for durable tracked work.
- `/attachments` for upload and download.
- `/dashboard` for the static dashboard bundle.

## Jobs Execution Policy

`POST /jobs` accepts `process_scope` and `continuity` for path/backend durable
jobs. Unassigned path/backend jobs default to `process_scope=per_fire` and
`continuity=resume`, so each run uses a short-lived executor process and later
runs can resume backend-native runtime context when a compatible runtime session
id is available. Use `continuity=fresh` to avoid backend resume.

## Jobs List Views

`GET /jobs` returns the full durable-work list by default. Dashboard-style clients that only need row data can use:

```http
GET /jobs?view=summary
```

The summary view keeps the same `{ "work": [...], "recurring": [...] }` envelope and preserves ids, state, timestamps, ownership/routing fields, result summaries, and trimmed execution target/delivery metadata. It omits heavier detail fields such as full requests, provenance, runner state, prompt bodies, and progress history. Fetch `GET /jobs/{id}/status` for the selected job or recurring `cal-*` template when full detail is needed.

## Auth

When `daemon.auth_token` is configured, clients send:

```http
Authorization: Bearer <token>
```

## Related

- [Python client](python-client.md)
- [CLI](cli.md)
- [Operations: architecture](../operations/architecture.md)
