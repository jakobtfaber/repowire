"""Antigravity CLI (`agy`) installer — plugin-based hook registration.

The Antigravity CLI is a separate product from Gemini CLI (separate binary
`agy`, separate auth, separate app-data directory at
`~/.gemini/antigravity-cli/`). Its extension surface is the plugin system:
`agy plugin install <dir>` copies a plugin directory into
`~/.gemini/antigravity-cli/plugins/<name>/` and records it in
`import_manifest.json`.

Verified plugin layout (`agy plugin validate`):
- `plugin.json` at the root with at least `{"name": "..."}`
- `hooks/hooks.json` with a Gemini-style hook schema (SessionStart /
  BeforeAgent / AfterAgent, each holding an array of
  `{"matcher": "...", "hooks": [{"type": "command", "command": "..."}]}`).

The MCP server registration shape for plugins is not yet documented and could
not be confirmed via `agy plugin validate` (it accepts unknown directory
layouts as "skipped"). We deliberately do not write `mcpServers/` entries until
the schema is verified — silent breakage is worse than an honest gap.

We file-drop directly into `~/.gemini/antigravity-cli/plugins/repowire/` and
update `import_manifest.json` rather than shelling out to `agy plugin install`.
This matches the installed-plugin layout produced by `agy` itself, keeps the
installer subprocess-free, and stays testable via tmp_path retargeting.
"""

from __future__ import annotations

import json
import subprocess
from datetime import datetime, timezone
from pathlib import Path

ANTIGRAVITY_HOME = Path.home() / ".gemini" / "antigravity-cli"
PLUGINS_DIR = ANTIGRAVITY_HOME / "plugins"
MANIFEST_PATH = ANTIGRAVITY_HOME / "import_manifest.json"
PLUGIN_NAME = "repowire"
PLUGIN_VERSION = "0.1.0"

# Hook events match Gemini CLI naming. If Antigravity wires the plugin hooks
# subsystem the same way Gemini does, the normalize() adapter already handles
# BeforeAgent/AfterAgent. Until that's confirmed end-to-end, treat actual
# hook firing as pending upstream verification.
_HOOKS_PAYLOAD: dict[str, list[dict]] = {
    "SessionStart": [
        {
            "matcher": "startup",
            "hooks": [
                {"type": "command", "command": "repowire hook session --backend=antigravity"},
            ],
        },
    ],
    # Gemini-style SessionEnd (deregistration at quit); inert until the
    # antigravity hooks subsystem fires, like the rest of this payload.
    "SessionEnd": [
        {
            "hooks": [
                {"type": "command", "command": "repowire hook session --backend=antigravity"},
            ],
        },
    ],
    "BeforeAgent": [
        {
            "hooks": [
                {"type": "command", "command": "repowire hook prompt --backend=antigravity"},
            ],
        },
    ],
    "AfterAgent": [
        {
            "hooks": [
                {"type": "command", "command": "repowire hook stop --backend=antigravity"},
            ],
        },
    ],
}


def _plugin_dir() -> Path:
    return PLUGINS_DIR / PLUGIN_NAME


def _write_json(path: Path, data: dict) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    tmp = path.with_suffix(path.suffix + ".tmp")
    tmp.write_text(json.dumps(data, indent=2))
    tmp.replace(path)


def _update_manifest(present: bool) -> None:
    """Add or remove the repowire entry in agy's import_manifest.json."""
    manifest: dict = {"imports": []}
    if MANIFEST_PATH.exists():
        try:
            manifest = json.loads(MANIFEST_PATH.read_text())
        except json.JSONDecodeError:
            manifest = {"imports": []}
    # `agy` writes "imports": null after an empty uninstall; treat that as [].
    existing = manifest.get("imports") or []
    imports = [e for e in existing if e.get("name") != PLUGIN_NAME]
    if present:
        imports.append({
            "name": PLUGIN_NAME,
            "source": "local-install",
            "importedAt": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
            "components": ["installed"],
        })
    manifest["imports"] = imports
    _write_json(MANIFEST_PATH, manifest)


def install_hooks() -> bool:
    """Install the repowire plugin into the Antigravity CLI plugins directory."""
    plugin_dir = _plugin_dir()
    _write_json(
        plugin_dir / "plugin.json",
        {
            "name": PLUGIN_NAME,
            "version": PLUGIN_VERSION,
            "description": "Repowire mesh integration (peer-to-peer agent messaging)",
        },
    )
    _write_json(plugin_dir / "hooks" / "hooks.json", _HOOKS_PAYLOAD)
    _update_manifest(present=True)
    return True


def uninstall_hooks() -> bool:
    """Remove the repowire plugin directory and manifest entry."""
    plugin_dir = _plugin_dir()
    removed = False
    if plugin_dir.exists():
        for entry in sorted(plugin_dir.rglob("*"), reverse=True):
            if entry.is_file() or entry.is_symlink():
                entry.unlink()
            else:
                entry.rmdir()
        plugin_dir.rmdir()
        removed = True
    if MANIFEST_PATH.exists():
        try:
            manifest = json.loads(MANIFEST_PATH.read_text())
        except json.JSONDecodeError:
            manifest = {"imports": []}
        existing = manifest.get("imports") or []
        manifest["imports"] = [e for e in existing if e.get("name") != PLUGIN_NAME]
        if len(manifest["imports"]) < len(existing):
            removed = True
        if manifest["imports"]:
            _write_json(MANIFEST_PATH, manifest)
        else:
            MANIFEST_PATH.unlink()
    return removed


def check_hooks_installed() -> bool:
    """Return True if both plugin files and the manifest entry are present."""
    plugin_dir = _plugin_dir()
    if not (plugin_dir / "plugin.json").exists():
        return False
    if not (plugin_dir / "hooks" / "hooks.json").exists():
        return False
    if not MANIFEST_PATH.exists():
        return False
    try:
        manifest = json.loads(MANIFEST_PATH.read_text())
    except json.JSONDecodeError:
        return False
    return any(e.get("name") == PLUGIN_NAME for e in (manifest.get("imports") or []))


def check_mcp_installed() -> bool:
    """MCP plugin schema for Antigravity is not yet verified.

    Always returns False so the doctor/status surface reports this honestly
    as a pending capability rather than claiming end-to-end MCP wiring works.
    """
    return False


def get_antigravity_version() -> tuple[int, ...] | None:
    """Get Antigravity CLI version as a tuple, or None if not installed.

    `agy changelog` prints the changelog with the current version on the
    first line as `X.Y.Z:`. There is no `--version` flag today.
    """
    try:
        result = subprocess.run(
            ["agy", "changelog"], capture_output=True, text=True, timeout=5,
        )
        if result.returncode != 0:
            return None
        first = result.stdout.strip().splitlines()[0] if result.stdout.strip() else ""
        ver = first.rstrip(":").strip()
        if not ver or not ver[0].isdigit():
            return None
        return tuple(int(x) for x in ver.split("."))
    except (FileNotFoundError, subprocess.TimeoutExpired, ValueError, IndexError):
        return None
