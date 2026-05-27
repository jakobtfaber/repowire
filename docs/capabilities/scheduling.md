# Scheduling

## What it does

Scheduling sends a future mesh message to yourself, another peer, or an orchestrator. Deliveries can be one-shot or recurring cron schedules.

## Where available

- CLI: `repowire schedule ...`
- MCP: `schedule_create`, `schedule_self`, `schedule_cron`, `schedule_list`, `schedule_delete`

## Common flows

Use one-shot schedules for future reminders and check-ins:

```bash
repowire schedule self 10m "check CI"
```

Use recurring schedules for ongoing nudges:

```bash
repowire schedule cron orchestrator "@daily" "review open jobs"
```

Use `kind=ask` only when every delivery should require an explicit `ack`. Open asks continue to resurface through the ask reminder path until closed.

## Limits

Schedules are message deliveries, not full task state. Use jobs when the work needs durable status, result, or cancellation semantics.

## Related

- [Guide: schedule work](../guides/schedule-work.md)
- [Concepts: jobs and schedules](../concepts/jobs-and-schedules.md)
- [CLI reference: schedule](../reference/cli.md#repowire-schedule)
- [MCP tools: scheduling](../reference/mcp-tools.md#scheduling)
