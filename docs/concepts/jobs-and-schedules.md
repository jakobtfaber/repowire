# Jobs and Schedules

Schedules deliver future mesh messages. Jobs represent durable tracked work that can be created, inspected, updated, completed, or canceled through CLI and MCP surfaces.

## Schedules

Use schedules for reminders, future asks, and recurring check-ins:

```bash
repowire schedule self 10m "check CI"
repowire schedule cron orchestrator "@daily" "review open jobs"
```

Scheduling is message-oriented: a delivery can be a fire-and-forget notification or a tracked ask that requires an `ack`.

## Jobs

Jobs are durable work records. They are useful when an orchestrator or human needs to track status, result, cancellation, and recurring worker templates across turns.

```bash
repowire jobs create "Daily brief" --path .repowire/agents/daily-brief --backend codex --cron "@daily" --prompt "Prepare the brief."
```

For recurring path/backend jobs, each fire uses a short-lived executor process.
`continuity=resume` keeps the backend-native runtime session id as the
continuity handle for the next fire; `continuity=fresh` starts without that
resume handle. The process is released after terminal job completion.

## Related

- [Capabilities: scheduling](../capabilities/scheduling.md)
- [Capabilities: jobs](../capabilities/jobs.md)
- [Guide: run jobs](../guides/run-jobs.md)
- [CLI reference](../reference/cli.md#repowire-jobs)
- [MCP tools: scheduling](../reference/mcp-tools.md#scheduling)
