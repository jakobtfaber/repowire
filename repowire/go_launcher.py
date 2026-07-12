"""Wheel entry point: replace this process with the bundled Go binary."""

from __future__ import annotations

import os
import shutil
import subprocess
import sys
from pathlib import Path


def _binary() -> Path | None:
    explicit = os.environ.get("REPOWIRE_HUB_BIN", "").strip()
    if explicit:
        path = Path(explicit)
        return path if path.is_file() else None
    root = Path(__file__).resolve().parent.parent
    source = root / "daemon-go"
    if (source / "go.mod").is_file() and shutil.which("go"):
        target = Path.home() / ".cache" / "repowire" / "bin" / "repowire"
        target.parent.mkdir(parents=True, exist_ok=True)
        newest_source = max(path.stat().st_mtime for path in source.rglob("*.go"))
        if not target.is_file() or target.stat().st_mtime < newest_source:
            subprocess.run(["go", "build", "-o", target, "."], cwd=source, check=True)
        return target
    bundled = Path(__file__).parent / "_bin" / "repowire-hub-go"
    if bundled.is_file():
        return bundled
    if found := shutil.which("repowire-hub-go"):
        return Path(found)
    return None


def main() -> None:
    binary = _binary()
    if binary is None:
        raise SystemExit(
            "Repowire's Go binary is missing. Reinstall the platform wheel or set "
            "REPOWIRE_HUB_BIN to a built daemon-go binary."
        )
    binary.chmod(binary.stat().st_mode | 0o111)
    # Preserve the owning Python/tool environment for the Go CLI's explicit
    # self-update command; os.Executable() inside Go points at the bundled binary.
    os.environ["REPOWIRE_PYTHON"] = sys.executable
    os.execv(binary, [str(binary), *sys.argv[1:]])
