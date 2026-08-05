from __future__ import annotations

import os
import sys
from pathlib import Path


def main() -> None:
    environment = os.environ.copy()
    environment["REPOWIRE_CLAUDE_OPT_IN"] = "1"
    config = Path.home() / ".repowire" / "claude-mcp.json"
    if not config.is_file():
        raise SystemExit("rwclaude: Repowire MCP config missing; run `repowire setup`")
    user_arguments = sys.argv[1:]
    try:
        delimiter = user_arguments.index("--")
    except ValueError:
        delimiter = len(user_arguments)
    arguments = [
        "claude",
        *user_arguments[:delimiter],
        "--mcp-config",
        str(config),
        *user_arguments[delimiter:],
    ]
    try:
        os.execvpe("claude", arguments, environment)
    except FileNotFoundError as error:
        raise SystemExit("rwclaude: Claude Code executable not found") from error
