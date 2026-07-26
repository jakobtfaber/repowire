from __future__ import annotations

import json
import shlex
import shutil
import subprocess
from pathlib import Path

from repowire.config.models import load_config

CLAUDE_SETTINGS = Path.home() / ".claude" / "settings.json"
CLAUDE_JSON = Path.home() / ".claude.json"

HOOK_EVENTS = [
    "Stop", "StopFailure", "SessionStart", "SessionEnd",
    "UserPromptSubmit", "Notification", "PreToolUse",
]

# Per-hook timeout (seconds) for the blocking PreToolUse approval. Must sit
# above the daemon wait + client margin so the hook isn't killed mid-wait.
PRETOOLUSE_HOOK_TIMEOUT_SECONDS = 60

# Channel transport requires Claude Code v2.1.80+ with claude.ai login
CHANNEL_MIN_VERSION = (2, 1, 80)
HOOK_MANIFEST_TIMEOUT_SECONDS = 2


def _load_claude_settings() -> dict:
    if not CLAUDE_SETTINGS.exists():
        return {}
    try:
        with open(CLAUDE_SETTINGS) as f:
            return json.load(f)
    except json.JSONDecodeError as e:
        raise RuntimeError(
            f"Corrupted settings.json at {CLAUDE_SETTINGS}: {e}. "
            "Please fix or delete the file manually."
        ) from e


def _save_claude_settings(settings: dict) -> None:
    CLAUDE_SETTINGS.parent.mkdir(parents=True, exist_ok=True)
    with open(CLAUDE_SETTINGS, "w") as f:
        json.dump(settings, f, indent=2)


def _make_hook_config(command: str) -> dict:
    return {
        "hooks": [
            {
                "type": "command",
                "command": command,
            }
        ]
    }


def _make_notification_hook_config(command: str, matcher: str) -> dict:
    return {
        "matcher": matcher,
        "hooks": [
            {
                "type": "command",
                "command": command,
            }
        ],
    }


def _make_pretooluse_hook_config(command: str, matcher: str, timeout: int) -> dict:
    return {
        "matcher": matcher,
        "hooks": [
            {
                "type": "command",
                "command": command,
                "timeout": timeout,
            }
        ],
    }


def _is_repowire_hook_entry(entry: dict) -> bool:
    for hook in entry.get("hooks", []):
        if "repowire" in hook.get("command", ""):
            return True
    return False


def _is_repowire_child_command(command: str) -> bool:
    try:
        argv = shlex.split(command)
    except ValueError:
        return False
    return any(
        Path(token).name == "repowire" and argv[index + 1:index + 2] == ["hook"]
        for index, token in enumerate(argv)
    )


def _is_direct_repowire_hook_entry(entry: dict) -> bool:
    return any(
        isinstance(command := hook.get("command"), str)
        and _is_repowire_child_command(command)
        for hook in entry.get("hooks", [])
    )


def _external_repowire_hooks(settings: dict) -> tuple[set[str], set[str]]:
    events: set[str] = set()
    dispatchers: set[str] = set()
    candidates: dict[tuple[str, ...], tuple[set[str], set[str]]] = {}
    for event, entries in settings.get("hooks", {}).items():
        if not isinstance(entries, list):
            continue
        for entry in entries:
            for hook in entry.get("hooks", []):
                command = hook.get("command")
                if not isinstance(command, str):
                    continue
                if _is_repowire_child_command(command):
                    continue
                try:
                    argv = shlex.split(command)
                except ValueError:
                    continue
                if len(argv) < 2 or argv[-1] != event:
                    continue
                base = tuple(argv[:-1])
                owned_events, commands = candidates.setdefault(base, (set(), set()))
                owned_events.add(event)
                commands.add(command)

    for base, (owned_events, commands) in candidates.items():
        if not set(_REQUIRED_HOOK_EVENTS) <= owned_events:
            continue
        executable = str(Path(base[0]).expanduser())
        try:
            result = subprocess.run(
                [executable, *base[1:], "--manifest-json"],
                capture_output=True,
                text=True,
                timeout=HOOK_MANIFEST_TIMEOUT_SECONDS,
            )
            if result.returncode != 0:
                continue
            manifest = json.loads(result.stdout)
        except (
            json.JSONDecodeError,
            OSError,
            subprocess.TimeoutExpired,
            UnicodeError,
            ValueError,
        ):
            continue
        if (
            not isinstance(manifest, dict)
            or manifest.get("schema_version") != 1
            or not isinstance(manifest.get("hooks"), dict)
        ):
            continue
        manifest_hooks = manifest["hooks"]
        if not all(
            isinstance(name, str)
            and isinstance(child_commands, list)
            and all(isinstance(child, str) for child in child_commands)
            for name, child_commands in manifest_hooks.items()
        ):
            continue
        events.update(
            name
            for name, child_commands in manifest_hooks.items()
            if any(_is_repowire_child_command(child) for child in child_commands)
        )
        dispatchers.update(commands)
    return events, dispatchers


def _replace_repowire_hook(settings: dict, event: str, entry: dict | None) -> None:
    hooks = settings.setdefault("hooks", {})
    existing = hooks.get(event, [])
    if not isinstance(existing, list):
        existing = []
    existing = [item for item in existing if not _is_repowire_hook_entry(item)]
    if entry is not None:
        existing.append(entry)
    if existing:
        hooks[event] = existing
    else:
        hooks.pop(event, None)


def install_hooks(channel_mode: bool = False) -> bool:
    """Install hooks. In channel_mode, only install Stop hook for dashboard chat_turns."""
    settings = _load_claude_settings()
    external_events, _ = _external_repowire_hooks(settings)
    requested_events = (
        set(_CHANNEL_HOOK_EVENTS) if channel_mode else set(_REQUIRED_HOOK_EVENTS)
    )
    if channel_mode and external_events - set(_CHANNEL_HOOK_EVENTS):
        raise RuntimeError(
            "Cannot enable channel mode while an external dispatcher provides "
            "the full Repowire lifecycle hook transport."
        )
    if requested_events <= external_events:
        return True

    # Stop hook always needed (dashboard chat_turns)
    _replace_repowire_hook(settings, "Stop", _make_hook_config("repowire hook stop"))
    _replace_repowire_hook(settings, "StopFailure", _make_hook_config("repowire hook stop"))

    if not channel_mode:
        # Full hook set for tmux transport
        _replace_repowire_hook(
            settings, "SessionStart", _make_hook_config("repowire hook session")
        )
        _replace_repowire_hook(settings, "SessionEnd", _make_hook_config("repowire hook session"))
        _replace_repowire_hook(
            settings, "UserPromptSubmit", _make_hook_config("repowire hook prompt")
        )
        _replace_repowire_hook(
            settings,
            "Notification",
            _make_notification_hook_config("repowire hook notification", "idle_prompt"),
        )
        # PreToolUse remote approval: opt-in only. Register the hook (matcher
        # scoped to the configured gated tools) when enabled, otherwise strip
        # any prior repowire PreToolUse entry so toggling off cleans up.
        approval = load_config().experiments.remote_tool_approval
        if approval.enabled and approval.gated_tools:
            matcher = "|".join(approval.gated_tools)
            _replace_repowire_hook(
                settings,
                "PreToolUse",
                _make_pretooluse_hook_config(
                    "repowire hook pretooluse", matcher, PRETOOLUSE_HOOK_TIMEOUT_SECONDS,
                ),
            )
        else:
            _replace_repowire_hook(settings, "PreToolUse", None)
    else:
        for event in (
            "SessionStart", "SessionEnd", "UserPromptSubmit", "Notification", "PreToolUse",
        ):
            _replace_repowire_hook(settings, event, None)

    if not settings.get("hooks"):
        settings.pop("hooks", None)
    _save_claude_settings(settings)
    return True


def uninstall_hooks() -> bool:
    """Remove repowire hooks. Returns True if hooks were removed, False if none existed."""
    settings = _load_claude_settings()

    if "hooks" not in settings:
        return False

    removed_any = False
    _, external_dispatchers = _external_repowire_hooks(settings)
    for event in HOOK_EVENTS:
        entries = settings["hooks"].get(event, [])
        if not isinstance(entries, list):
            continue
        filtered = [
            entry for entry in entries
            if any(
                hook.get("command") in external_dispatchers
                for hook in entry.get("hooks", [])
            )
            or not _is_direct_repowire_hook_entry(entry)
        ]
        if len(filtered) < len(entries):
            removed_any = True
            if filtered:
                settings["hooks"][event] = filtered
            else:
                del settings["hooks"][event]

    if not settings["hooks"]:
        del settings["hooks"]

    if removed_any:
        _save_claude_settings(settings)
    return removed_any


# PreToolUse is opt-in (remote tool approval); its presence is not required for
# hooks to count as installed. uninstall still iterates HOOK_EVENTS to clean it.
_REQUIRED_HOOK_EVENTS = [e for e in HOOK_EVENTS if e != "PreToolUse"]
_CHANNEL_HOOK_EVENTS = ["Stop", "StopFailure"]


def check_hooks_installed(channel_mode: bool = False) -> bool:
    settings = _load_claude_settings()
    if "hooks" not in settings:
        return False

    external_events, _ = _external_repowire_hooks(settings)
    required_events = _CHANNEL_HOOK_EVENTS if channel_mode else _REQUIRED_HOOK_EVENTS
    return all(
        event in external_events
        or any(_is_repowire_hook_entry(entry) for entry in settings["hooks"].get(event, []))
        for event in required_events
    )


def check_configured_hooks_installed() -> bool:
    """Check the hook subset required by the currently configured transport."""
    return check_hooks_installed(channel_mode=check_channel_installed())


# -- Channel transport --


def get_claude_version() -> tuple[int, ...] | None:
    """Get Claude Code version as a tuple, or None if not installed."""
    try:
        result = subprocess.run(
            ["claude", "--version"], capture_output=True, text=True, timeout=5,
        )
        if result.returncode != 0:
            return None
        # Output like "2.1.81 (Claude Code)"
        version_str = result.stdout.strip().split()[0]
        return tuple(int(x) for x in version_str.split("."))
    except (FileNotFoundError, subprocess.TimeoutExpired, ValueError, IndexError):
        return None


def supports_channels() -> bool:
    """Check if Claude Code supports the channel transport."""
    version = get_claude_version()
    if not version:
        return False
    return version >= CHANNEL_MIN_VERSION


def _find_channel_server() -> Path | None:
    """Find the channel server.ts in the installed package."""
    # Check installed package location
    import repowire

    pkg_dir = Path(repowire.__file__).parent
    server = pkg_dir / "channel" / "server.ts"
    if server.exists():
        return server
    return None


def _has_bun() -> bool:
    """Check if bun runtime is available."""
    return shutil.which("bun") is not None


def install_channel() -> tuple[bool, str]:
    """Install the channel transport (experimental). Returns (success, message).

    Requires claude.ai login (not API/Console key auth).
    Gracefully falls back with a clear message if:
    - Claude Code version too old
    - bun not installed
    - Channel server not found
    """
    if not _has_bun():
        return False, "bun runtime not found. Install from https://bun.sh"

    if not supports_channels():
        version = get_claude_version()
        v_str = ".".join(str(x) for x in version) if version else "unknown"
        return False, (
            f"Claude Code {v_str} doesn't support channels "
            f"(requires {'.'.join(str(x) for x in CHANNEL_MIN_VERSION)}+). "
            "Using hooks instead."
        )

    server_path = _find_channel_server()
    if not server_path:
        return False, "Channel server.ts not found in package."

    # Install deps (bun install is idempotent — fast no-op if already installed)
    try:
        result = subprocess.run(
            ["bun", "install"], cwd=str(server_path.parent),
            capture_output=True, timeout=30,
        )
        if result.returncode != 0:
            return False, f"bun install failed: {result.stderr.decode()[:200]}"
    except (subprocess.TimeoutExpired, FileNotFoundError):
        return False, "Failed to install channel dependencies."

    # Add to ~/.claude.json (user-level MCP config)
    try:
        config = json.loads(CLAUDE_JSON.read_text())
    except (FileNotFoundError, json.JSONDecodeError):
        config = {}
    config.setdefault("mcpServers", {})

    channel_entry: dict[str, object] = {
        "command": "bun",
        "args": [str(server_path)],
    }
    auth_token = load_config().daemon.auth_token
    if auth_token:
        channel_entry["env"] = {"REPOWIRE_AUTH_TOKEN": auth_token}

    config["mcpServers"]["repowire-channel"] = channel_entry

    CLAUDE_JSON.write_text(json.dumps(config, indent=2))

    return True, (
        "Channel transport installed. "
        "Start Claude with: claude --channels server:repowire-channel"
    )


def uninstall_channel() -> bool:
    """Remove the channel from ~/.claude.json."""
    if not CLAUDE_JSON.exists():
        return False

    try:
        config = json.loads(CLAUDE_JSON.read_text())
    except json.JSONDecodeError:
        return False

    servers = config.get("mcpServers", {})
    if "repowire-channel" not in servers:
        return False

    del servers["repowire-channel"]
    if not servers:
        del config["mcpServers"]

    CLAUDE_JSON.write_text(json.dumps(config, indent=2))
    return True


def check_channel_installed() -> bool:
    """Check if the configured channel can start from this installed package."""
    if not CLAUDE_JSON.exists():
        return False
    try:
        config = json.loads(CLAUDE_JSON.read_text())
    except json.JSONDecodeError:
        return False
    servers = config.get("mcpServers")
    if not isinstance(servers, dict):
        return False
    entry = servers.get("repowire-channel")
    if not isinstance(entry, dict):
        return False
    command = entry.get("command")
    if not isinstance(command, str):
        return False
    resolved_command = shutil.which(command)
    if not resolved_command or Path(resolved_command).resolve().name != "bun":
        return False
    if not supports_channels():
        return False
    server = _find_channel_server()
    if server is None or not server.is_file():
        return False
    if entry.get("args") != [str(server)]:
        return False
    package_path = server.parent / "package.json"
    try:
        package = json.loads(package_path.read_text())
    except (OSError, json.JSONDecodeError):
        return False
    if not isinstance(package, dict) or package.get("name") != "repowire-channel":
        return False
    declared_dependencies = package.get("dependencies")
    required_dependencies = {"@modelcontextprotocol/sdk", "ws", "zod"}
    if (
        not isinstance(declared_dependencies, dict)
        or not required_dependencies <= declared_dependencies.keys()
        or not all(
            isinstance(declared_dependencies[name], str)
            and bool(declared_dependencies[name])
            for name in required_dependencies
        )
    ):
        return False
    dependencies = (
        server.parent / "node_modules" / "@modelcontextprotocol" / "sdk",
        server.parent / "node_modules" / "ws",
        server.parent / "node_modules" / "zod",
    )
    if not all(dependency.is_dir() for dependency in dependencies):
        return False
    env = entry.get("env")
    if env is not None and not isinstance(env, dict):
        return False
    try:
        expected_auth_token = load_config().daemon.auth_token
    except Exception:
        return False
    if expected_auth_token and (
        not isinstance(env, dict)
        or env.get("REPOWIRE_AUTH_TOKEN") != expected_auth_token
    ):
        return False
    return True
