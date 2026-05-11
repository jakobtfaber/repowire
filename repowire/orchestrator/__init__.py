"""Orchestrator workspace management.

The orchestrator is a productized version of a hand-rolled pattern — a
dedicated mesh peer that coordinates work across other peers. Its workspace
lives at `~/.repowire/orchestrator/` and is scaffolded from a bundled
template at `repowire/orchestrator/template/`.

See GitHub issue #38 and `/Users/prass/.claude/plans/plan-it-out-glittery-bentley.md`.
"""

from repowire.orchestrator.workspace import (
    backup_workspace,
    init_workspace,
    is_installed,
    update_workspace,
    validate_workspace,
    workspace_path,
)

__all__ = [
    "backup_workspace",
    "init_workspace",
    "is_installed",
    "update_workspace",
    "validate_workspace",
    "workspace_path",
]
