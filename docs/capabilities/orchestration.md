# Orchestration

## What it does

An orchestrator is a peer whose job is coordinating other peers. It can dispatch asks, collect status, review queues, schedule check-ins, and keep multi-repo work moving.

## Where available

- Any normal agent session can act as an orchestrator.
- MCP includes orchestration helpers such as `claim_orchestrator_role`, `orchestrator_status`, `review_queue`, and scheduling tools.
- CLI includes `repowire peer claim-role orchestrator` and `repowire orchestrator persona ...` helpers.

## Common flows

- Claim or repair the orchestrator role for a long-running coordinator peer.
- Use `orchestrator_status` before dispatching long-running work.
- Route review work through `review_queue` and `mark_reviewed`.
- Schedule check-ins so the orchestrator wakes itself or workers later.
- Pair the orchestrator with a persona when routing policy should be explicit.

## Limits

The orchestrator is a workflow pattern, not a magic global scheduler. It works best when the human gives it explicit goals, scope, and handoff expectations.

## Related

- [Concepts: orchestrator pattern](../concepts/orchestrator.md)
- [Concepts: personas](../concepts/personas.md)
- [Pattern: orchestrator coordination](../patterns/orchestrator-coordination.md)
- [CLI reference: orchestrator persona](../reference/cli.md#repowire-orchestrator-persona)
- [MCP tools: orchestrator_status](../reference/mcp-tools.md#orchestrator_status)
