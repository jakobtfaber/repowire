# Documentation Standards

Feature work that changes public behavior must update public docs in the same PR.

## Where changes go

- README: install path, quickstart, supported agents, major features, screenshots, roadmap positioning.
- Concepts and patterns: mental models and recommended workflows.
- Guides: task-oriented setup and operating recipes.
- Capabilities: behavior and limits of user-facing features.
- Operations: daemon, relay, transports, state, deployment, and security.
- Reference: exact CLI, MCP, Python client, config, HTTP, WebSocket, and hook details.
- Troubleshooting: symptom-oriented fixes.
- Contributing: maintainer workflows, release/versioning, backend additions, and design notes.

## Before opening a PR

```bash
uv run --no-project mkdocs build --strict
python3 scripts/pre_pr_hygiene.py
```

If docs are intentionally deferred, file a Beads follow-up and say why in the PR handoff.

## Related

- [Pre-PR hygiene](pre-pr-hygiene.md)
- [Design system](design-system.md)
