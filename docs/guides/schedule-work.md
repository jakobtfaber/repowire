# Schedule Work

## Goal

Wake yourself, another peer, or an orchestrator later with a notification or tracked ask.

## Steps

```bash
repowire schedule self 10m "check CI"
repowire schedule create orchestrator 1h "handoff" --from-peer project-a --kind ask
repowire schedule cron orchestrator "@daily" "review open jobs"
```

Use `kind=ask` only when the recipient should explicitly close each delivery. Use notify-style schedules for reminders and status nudges.

## Verify

```bash
repowire schedule list
```

Delete a schedule when it is no longer needed:

```bash
repowire schedule delete sched-...
```

## Related

- [Capabilities: scheduling](../capabilities/scheduling.md)
- [Concepts: jobs and schedules](../concepts/jobs-and-schedules.md)
- [CLI reference](../reference/cli.md#repowire-schedule)
