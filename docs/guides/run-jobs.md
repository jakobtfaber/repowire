# Run Jobs

## Goal

Create durable work that can be inspected, updated, completed, canceled, or repeated on a schedule.

## Before you start

Use jobs when the work needs status and result tracking. Use [schedules](schedule-work.md) when you only need a future message, and use `ask` when you only need a peer to answer one question.

## Steps

Create a job:

```bash
repowire jobs create "Daily brief" --path .repowire/agents/daily-brief --backend codex --cron "@daily" --prompt "Prepare the brief."
```

List and inspect jobs:

```bash
repowire jobs list
repowire jobs show job-...
```

Update status or record a result from CLI/MCP surfaces:

```bash
repowire jobs update job-... --state running --note "Started first pass"
repowire jobs update job-... --state succeeded --result-summary "Brief posted"
repowire jobs result job-...
```

Cancel work that should not continue:

```bash
repowire jobs cancel job-...
```

## Worker folders

`repowire agents create <name>` scaffolds a repo-local `.repowire/agents/<name>` folder for durable worker prompts and instructions. Target that folder from recurring jobs when the job should run as a reusable worker rather than an ad hoc prompt.

## Related

- [Capabilities: jobs](../capabilities/jobs.md)
- [Concepts: jobs and schedules](../concepts/jobs-and-schedules.md)
- [CLI reference: jobs](../reference/cli.md#repowire-jobs)
- [MCP tools: lifecycle](../reference/mcp-tools.md#lifecycle)
