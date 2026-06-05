"""Tmux lifecycle hook registration.

Installs/uninstalls tmux hooks that POST to the daemon's
/hooks/lifecycle/* endpoints on pane/session/window events.

This is the ONLY module that knows about `tmux set-hook`.
"""

from __future__ import annotations

import logging
import shlex
import subprocess
from dataclasses import dataclass
from pathlib import Path
from typing import Literal

from repowire.hooks._tmux import is_tmux_available

logger = logging.getLogger(__name__)

# Re-export for callers that import from this module.
__all__ = [
    "HookInspection",
    "inspect_lifecycle_hooks",
    "is_tmux_available",
    "install_hooks",
    "uninstall_hooks",
]

HookStatus = Literal["ok", "missing", "empty", "wrong-command"]


@dataclass(frozen=True)
class HookInspection:
    hook_name: str
    status: HookStatus
    detail: str = ""
    command: str = ""

# Numeric array index — avoids clobbering user hooks at default index [0].
_HOOK_INDEX = 42

# Shell script for rename hooks (avoids tmux quoting hell with $()).
_RENAME_SCRIPT = Path(__file__).parent / "tmux_rename_hook.sh"

# Hook definitions: list of (tmux_hook_name, tmux_flag, shell_command).
#
# tmux_flag: "-g" for session-level hooks, "-gw" for window-level hooks.
# pane-exited (not pane-died, which requires remain-on-exit).
#
# Rename hooks call an external script because tmux's command parser
# can't handle $() subshells in run-shell arguments.
_HOOKS: list[tuple[str, str, str]] = [
    # -- pane exit --
    (
        "pane-exited",
        "-gw",
        "curl -sf -o /dev/null -X POST http://{host}:{port}/hooks/lifecycle/pane-died"
        ' -H "Content-Type: application/json"'
        ' -d "{\\"pane_id\\":\\"#{hook_pane}\\"}"',
    ),
    # -- session close --
    (
        "session-closed",
        "-g",
        "curl -sf -o /dev/null -X POST"
        " http://{host}:{port}/hooks/lifecycle/session-closed"
        ' -H "Content-Type: application/json"'
        ' -d "{\\"session_name\\":\\"#{session_name}\\"}"',
    ),
    # -- session rename (post-rename, via helper script) --
    (
        "after-rename-session",
        "-g",
        "{script}"
        " http://{host}:{port}/hooks/lifecycle/session-renamed"
        " #{session_name} '' -s",
    ),
    # -- window rename (post-rename, via helper script) --
    (
        "after-rename-window",
        "-gw",
        "{script}"
        " http://{host}:{port}/hooks/lifecycle/window-renamed"
        " #{window_name} #{session_name} ''",
    ),
    # -- client detach --
    (
        "client-detached",
        "-g",
        "curl -sf -o /dev/null -X POST"
        " http://{host}:{port}/hooks/lifecycle/client-detached"
        ' -H "Content-Type: application/json"'
        ' -d "{\\"session_name\\":\\"#{session_name}\\"}"',
    ),
]

_HOOK_FINGERPRINTS: dict[str, tuple[str, ...]] = {
    "pane-exited": ("/hooks/lifecycle/pane-died",),
    "session-closed": ("/hooks/lifecycle/session-closed",),
    "after-rename-session": (
        "tmux_rename_hook.sh",
        "/hooks/lifecycle/session-renamed",
    ),
    "after-rename-window": (
        "tmux_rename_hook.sh",
        "/hooks/lifecycle/window-renamed",
    ),
    "client-detached": ("/hooks/lifecycle/client-detached",),
}


def _hook_slot(hook_name: str) -> str:
    return f"{hook_name}[{_HOOK_INDEX}]"


def _inspect_hook(hook_name: str, flag: str) -> HookInspection:
    slot = _hook_slot(hook_name)
    try:
        result = subprocess.run(
            ["tmux", "show-hooks", flag, slot],
            capture_output=True,
            text=True,
            timeout=5,
        )
    except (subprocess.TimeoutExpired, FileNotFoundError) as exc:
        return HookInspection(hook_name, "missing", str(exc))

    if result.returncode != 0:
        return HookInspection(hook_name, "missing", result.stderr.strip())

    command = ""
    for line in result.stdout.splitlines():
        stripped = line.strip()
        if stripped == slot:
            command = ""
            break
        if stripped.startswith(f"{slot} "):
            command = stripped[len(slot):].strip()
            break

    if not command:
        return HookInspection(hook_name, "empty", "hook slot has no command")

    missing = [
        needle for needle in _HOOK_FINGERPRINTS[hook_name]
        if needle not in command
    ]
    if missing:
        detail = f"missing fingerprint(s): {', '.join(missing)}"
        return HookInspection(hook_name, "wrong-command", detail, command)

    return HookInspection(hook_name, "ok", command=command)


def inspect_lifecycle_hooks() -> list[HookInspection]:
    """Inspect Repowire's tmux lifecycle hook slots without changing them."""
    return [_inspect_hook(hook_name, flag) for hook_name, flag, _ in _HOOKS]


def install_hooks(host: str = "127.0.0.1", port: int = 8377) -> list[str]:
    """Install tmux lifecycle hooks. Idempotent.

    Returns list of hook names successfully installed.
    """
    script = str(_RENAME_SCRIPT)
    installed: list[str] = []
    for hook_name, flag, cmd_template in _HOOKS:
        cmd = (
            cmd_template
            .replace("{host}", host)
            .replace("{port}", str(port))
            .replace("{script}", script)
        )
        tmux_cmd = f"run-shell -b -- {shlex.quote(cmd)}"
        result = subprocess.run(
            ["tmux", "set-hook", flag, f"{hook_name}[{_HOOK_INDEX}]", tmux_cmd],
            capture_output=True,
            text=True,
            timeout=5,
        )
        if result.returncode == 0:
            inspection = _inspect_hook(hook_name, flag)
            if inspection.status == "ok":
                installed.append(hook_name)
            else:
                logger.warning(
                    "Tmux hook %s set but verification failed: %s (%s)",
                    hook_name,
                    inspection.status,
                    inspection.detail,
                )
        else:
            logger.warning(
                "Failed to install tmux hook %s: %s",
                hook_name, result.stderr.strip(),
            )
    return installed


def uninstall_hooks() -> list[str]:
    """Remove all repowire tmux hooks.

    Returns list of hook names successfully removed.
    """
    removed: list[str] = []
    for hook_name, flag, _ in _HOOKS:
        unsetter = flag + "u"  # -g → -gu, -gw → -gwu
        result = subprocess.run(
            ["tmux", "set-hook", unsetter, f"{hook_name}[{_HOOK_INDEX}]"],
            capture_output=True,
            text=True,
            timeout=5,
        )
        if result.returncode == 0:
            removed.append(hook_name)
    return removed
