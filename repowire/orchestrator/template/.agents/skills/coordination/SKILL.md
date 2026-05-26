---
name: coordination
description: Use when orchestrating complex or multi-lane work, decomposing tasks across peers, choosing status/board surfaces, running mesh roundups, or keeping high-level progress visible without doing implementation work yourself.
---

# Coordination

Coordinate the work shape; peers do the implementation.

## Core loop

1. Clarify objective, success criteria, owner, and deadline when missing.
2. Split complex work into independent lanes by repo, worktree, risk, or review concern.
3. Pick the lightest status surface that will survive the work: existing board, GitHub issue/project, Linear/Jira, Beads, a repo checklist, or a simple markdown board.
4. Assign each lane to a peer, worktree, or durable job. Avoid overlapping write scopes; make one lane review-only if two peers would edit the same files.
5. Track only high-level state: owner, status, blocker, next action. Keep transcript details in session history, not the board.
6. Round up by impact and blocker, not activity count.

## Board guidance

Prefer the user's existing board. Do not invent a new tracker unless the user asks or the work is large enough that status would otherwise be lost.

Useful board columns are simple: Todo, In Progress, Blocked, Review, Done. Entries should describe the outcome, owner, blocker, and next action. Do not mirror every message or tool call.

## Mesh roundup

When the user asks where things stand, use `list_peers()` and ask relevant peers in parallel. Ask specific questions: blocker, next merge, needed decision. Compile the reply with the highest-impact item first. Keep Telegram/mobile updates short.

## Anti-patterns

- Serializing independent lanes behind the orchestrator.
- Letting the orchestrator drift into implementation.
- Writing board or memory updates just because a turn ended.
- Count-leading summaries that reward volume over impact.
