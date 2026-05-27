# Jobs

## What it does

Jobs are durable tracked work records. They let agents and humans create work, inspect status, update progress, record results, cancel work, and run recurring worker templates.

## Where available

- CLI: `repowire jobs ...`
- MCP: `job_create`, `job_list`, `job_status`, `job_show`, `job_update`, `job_result`, `job_cancel`
- Dashboard: Jobs view for listing durable work and recurring templates, then running, retrying, or cancelling eligible records.

## Common flows

- Create a one-off job when an orchestrator needs to track a piece of work across turns.
- Use recurring jobs for durable worker folders such as `.repowire/agents/daily-brief`.
- Update progress while work is running, then record a final result.
- Cancel stale or superseded jobs instead of leaving them ambiguous.

## Limits

Jobs complement asks and schedules. Use an ask for a question that needs a reply; use a schedule for a future message; use a job when status and result need to survive across turns.

## Related

- [Guide: run jobs](../guides/run-jobs.md)
- [Concepts: jobs and schedules](../concepts/jobs-and-schedules.md)
- [CLI reference: jobs](../reference/cli.md#repowire-jobs)
- [MCP tools: lifecycle](../reference/mcp-tools.md#lifecycle)
