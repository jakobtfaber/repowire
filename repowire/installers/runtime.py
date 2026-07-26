"""Resolve commands owned by the active Repowire Python environment."""

from __future__ import annotations

import os
import shutil
import sys
import sysconfig
from pathlib import Path


def repowire_console_entrypoint() -> str:
    """Return the Repowire console script from the active Python environment."""
    script_dirs = (
        Path(sys.executable).parent,
        Path(sysconfig.get_path("scripts")),
    )
    for script_dir in dict.fromkeys(script_dirs):
        resolved = shutil.which("repowire", path=str(script_dir))
        if resolved:
            return str(Path(resolved).resolve())
    invoked = Path(sys.argv[0])
    if (
        invoked.is_absolute()
        and invoked.name.lower() in {"repowire", "repowire.exe"}
        and invoked.is_file()
        and os.access(invoked, os.X_OK)
    ):
        return str(invoked.resolve())
    raise RuntimeError(
        "Cannot find the Repowire console entrypoint in the active Python environment"
    )
