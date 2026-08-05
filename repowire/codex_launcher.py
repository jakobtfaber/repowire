from __future__ import annotations

import os
import sys


def main() -> None:
    environment = os.environ.copy()
    environment["REPOWIRE_CODEX_OPT_IN"] = "1"
    environment.pop("OPENAI_API_KEY", None)
    environment.pop("CODEX_API_KEY", None)
    arguments = [
        "codex",
        "-c",
        "mcp_servers.repowire.enabled=true",
        *sys.argv[1:],
    ]
    os.execvpe("codex", arguments, environment)
