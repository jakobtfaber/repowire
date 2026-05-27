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

## Auth

When `daemon.auth_token` is configured, clients send:

```http
Authorization: Bearer <token>
```

## Related

- [Python client](python-client.md)
- [CLI](cli.md)
- [Operations: architecture](../operations/architecture.md)
