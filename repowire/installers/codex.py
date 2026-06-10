"""OpenAI Codex CLI installer — hooks and MCP server configuration."""

from __future__ import annotations

import hashlib
import json
import subprocess
from pathlib import Path

CODEX_HOME = Path.home() / ".codex"
HOOKS_PATH = CODEX_HOME / "hooks.json"
CONFIG_PATH = CODEX_HOME / "config.toml"

# NOTE: codex has no SessionEnd hook event (hooks/src/events/ upstream:
# session_start, stop, user_prompt_submit, tool/compact/subagent events).
# Quit deregistration for codex rides on the ws-hook agent-pid watcher.
HOOK_EVENTS = ["SessionStart", "Stop", "UserPromptSubmit"]
_MCP_ENV_LINE = 'env = { REPOWIRE_BACKEND = "codex" }'

# Codex's hook_event_key_label() values for the events we install.
_EVENT_LABELS = {
    "SessionStart": "session_start",
    "Stop": "stop",
    "UserPromptSubmit": "user_prompt_submit",
}
# Codex normalizes timeout_sec.unwrap_or(600) before hashing.
_CODEX_DEFAULT_TIMEOUT = 600


def _load_hooks() -> dict:
    if not HOOKS_PATH.exists():
        return {}
    try:
        return json.loads(HOOKS_PATH.read_text())
    except (json.JSONDecodeError, OSError):
        return {}


def _save_hooks(data: dict) -> None:
    CODEX_HOME.mkdir(parents=True, exist_ok=True)
    HOOKS_PATH.write_text(json.dumps(data, indent=2))


def _make_hook_entry(command: str, matcher: str | None = None) -> dict:
    entry: dict = {
        "hooks": [{"type": "command", "command": command}],
    }
    if matcher:
        entry["matcher"] = matcher
    return entry


_REPOWIRE_HOOKS = {
    "SessionStart": _make_hook_entry(
        "repowire hook session --backend=codex", matcher="startup|resume|clear",
    ),
    "Stop": _make_hook_entry("repowire hook stop --backend=codex"),
    "UserPromptSubmit": _make_hook_entry("repowire hook prompt --backend=codex"),
}


def trusted_hash_for(event: str, command: str, matcher: str | None) -> str:
    """Reproduce codex's hook trust fingerprint for a command hook.

    Codex hashes the canonical JSON (recursively sorted keys, compact
    separators) of its NormalizedHookIdentity: the event key label plus the
    flattened matcher group holding one normalized handler — None fields
    omitted (TOML cannot represent them), timeout defaulted to 600. See
    codex-rs/hooks/src/engine/discovery.rs::command_hook_hash and
    codex-rs/config/src/fingerprint.rs::version_for_toml.
    """
    identity: dict = {
        "event_name": _EVENT_LABELS[event],
        "hooks": [
            {
                "async": False,
                "command": command,
                "timeout": _CODEX_DEFAULT_TIMEOUT,
                "type": "command",
            }
        ],
    }
    if matcher:
        identity["matcher"] = matcher
    payload = json.dumps(identity, sort_keys=True, separators=(",", ":"))
    return "sha256:" + hashlib.sha256(payload.encode()).hexdigest()


def _upsert_hook_state(content: str, key: str, trusted_hash: str) -> str:
    """Insert or update one [hooks.state."<key>"] section in config.toml text.

    String-surgical like the rest of this installer — never rewrites the
    file wholesale, so user comments and unrelated sections survive.
    """
    header = f'[hooks.state."{key}"]'
    line = f'trusted_hash = "{trusted_hash}"'
    if header in content:
        lines = content.splitlines()
        out: list[str] = []
        in_section = False
        replaced = False
        for ln in lines:
            if ln.strip() == header:
                in_section = True
                out.append(ln)
                continue
            if in_section and ln.lstrip().startswith("["):
                if not replaced:
                    out.append(line)
                    replaced = True
                in_section = False
            if in_section and ln.strip().startswith("trusted_hash"):
                out.append(line)
                replaced = True
                continue
            out.append(ln)
        if in_section and not replaced:
            out.append(line)
        return "\n".join(out) + ("\n" if content.endswith("\n") else "")
    suffix = "" if (not content or content.endswith("\n")) else "\n"
    return f"{content}{suffix}\n{header}\n{line}\n"


def write_trusted_hashes(data: dict) -> None:
    """Pre-trust the repowire hook entries codex just had written.

    Codex silently skips untrusted hooks (HookTrustStatus::Untrusted handlers
    are never executed), so without this every hooks.json rewrite would
    disable repowire's codex transport until the user re-trusts in the TUI.
    Only repowire-owned entries are touched: the state key is positional
    ("path:event:group:handler"), computed from where our entry actually
    landed in the saved arrays.
    """
    hooks = data.get("hooks", {})
    content = CONFIG_PATH.read_text() if CONFIG_PATH.exists() else ""
    for event, label in _EVENT_LABELS.items():
        entries = hooks.get(event, [])
        for group_index, entry in enumerate(entries):
            if not _is_repowire_hook(entry):
                continue
            for handler_index, handler in enumerate(entry.get("hooks", [])):
                command = handler.get("command", "")
                if "repowire" not in command:
                    continue
                key = f"{HOOKS_PATH}:{label}:{group_index}:{handler_index}"
                content = _upsert_hook_state(
                    content, key, trusted_hash_for(event, command, entry.get("matcher"))
                )
    CONFIG_PATH.parent.mkdir(parents=True, exist_ok=True)
    CONFIG_PATH.write_text(content)


def _is_repowire_hook(entry: dict) -> bool:
    """Check if a hook entry belongs to repowire."""
    for h in entry.get("hooks", []):
        if "repowire" in h.get("command", ""):
            return True
    return False


def install_hooks() -> bool:
    """Install repowire hooks into ~/.codex/hooks.json.

    Appends to existing hook arrays rather than overwriting, preserving
    user-defined hooks for the same events. The installed entries are
    pre-trusted in config.toml's [hooks.state] — codex silently skips
    untrusted hooks, so a bare hooks.json rewrite would otherwise disable
    the codex transport until the user re-trusts in the TUI.

    Returns True when hooks.json content actually changed.
    """
    before = _load_hooks()
    data = json.loads(json.dumps(before)) if before else {}
    hooks = data.setdefault("hooks", {})

    for event, entry in _REPOWIRE_HOOKS.items():
        existing = hooks.get(event, [])
        # Remove any previous repowire entries, then append fresh
        existing = [e for e in existing if not _is_repowire_hook(e)]
        existing.append(entry)
        hooks[event] = existing

    # Drop entries for events codex doesn't support (a prior repowire version
    # wrote an inert SessionEnd group).
    stale = hooks.get("SessionEnd", [])
    if stale:
        kept = [e for e in stale if not _is_repowire_hook(e)]
        if kept:
            hooks["SessionEnd"] = kept
        else:
            del hooks["SessionEnd"]

    _save_hooks(data)
    write_trusted_hashes(data)
    return data != before


def uninstall_hooks() -> bool:
    """Remove repowire hooks from hooks.json, preserving user-defined hooks."""
    data = _load_hooks()
    hooks = data.get("hooks", {})
    if not hooks:
        return False

    removed = False
    for event in HOOK_EVENTS:
        entries = hooks.get(event, [])
        filtered = [e for e in entries if not _is_repowire_hook(e)]
        if len(filtered) < len(entries):
            removed = True
            if filtered:
                hooks[event] = filtered
            else:
                del hooks[event]

    if not hooks:
        data.pop("hooks", None)

    if removed:
        _save_hooks(data)
    return removed


def _enable_hooks_feature() -> None:
    """Enable the hooks feature flag in config.toml.

    Codex hooks default to false. We need features.hooks = true for them to fire.
    Codex 0.129.0 renamed `codex_hooks` to `hooks`. Migrate the legacy key
    away because current Codex releases warn whenever it is present.
    """
    CODEX_HOME.mkdir(parents=True, exist_ok=True)

    if CONFIG_PATH.exists():
        content = CONFIG_PATH.read_text()
    else:
        content = ""

    lines = content.splitlines()
    in_features = False
    saw_features = False
    features_insert_at: int | None = None
    has_hooks = False
    legacy_value: str | None = None
    out: list[str] = []

    for line in lines:
        stripped = line.strip()
        if stripped.startswith("[") and stripped.endswith("]"):
            in_features = stripped == "[features]"
            if in_features:
                saw_features = True
                features_insert_at = len(out) + 1
            out.append(line)
            continue
        if in_features:
            if stripped.startswith("hooks") and "=" in stripped:
                has_hooks = True
            if stripped.startswith("codex_hooks") and "=" in stripped:
                legacy_value = stripped.split("=", 1)[1].strip() or "true"
                continue
        out.append(line)

    if saw_features:
        if not has_hooks and features_insert_at is not None:
            out.insert(features_insert_at, f"hooks = {legacy_value or 'true'}")
        content = "\n".join(out).rstrip() + "\n"
    elif content:
        content = content.rstrip() + "\n\n[features]\nhooks = true\n"
    else:
        content = "[features]\nhooks = true\n"

    CONFIG_PATH.write_text(content)


def _ensure_repowire_mcp_backend_env(content: str) -> str:
    lines = content.splitlines()
    out: list[str] = []
    in_section = False
    saw_section = False
    saw_env = False

    for line in lines:
        stripped = line.strip()
        if stripped.startswith("[") and stripped.endswith("]"):
            if in_section and not saw_env:
                out.append(_MCP_ENV_LINE)
            in_section = stripped == "[mcp_servers.repowire]"
            saw_section = saw_section or in_section
            saw_env = False
            out.append(line)
            continue

        if in_section and stripped.startswith("env") and "=" in stripped:
            saw_env = True
            if "REPOWIRE_BACKEND" not in stripped and stripped.endswith("}"):
                prefix, suffix = line.rsplit("}", 1)
                sep = "" if prefix.rstrip().endswith("{") else ","
                line = f'{prefix}{sep} REPOWIRE_BACKEND = "codex" }}{suffix}'
        out.append(line)

    if in_section and not saw_env:
        out.append(_MCP_ENV_LINE)

    if not saw_section:
        return content
    return "\n".join(out).rstrip() + "\n"


def install_mcp() -> bool:
    """Add repowire MCP server to ~/.codex/config.toml.

    Appends the [mcp_servers.repowire] section. Preserves existing content.
    Also enables the hooks feature flag (required for hooks to fire).
    """
    CODEX_HOME.mkdir(parents=True, exist_ok=True)

    _enable_hooks_feature()

    # Use bare command name — resolved via PATH at runtime, survives upgrades
    section = (
        "\n[mcp_servers.repowire]\n"
        'command = "repowire"\n'
        'args = ["mcp"]\n'
        f"{_MCP_ENV_LINE}\n"
    )

    if CONFIG_PATH.exists():
        content = CONFIG_PATH.read_text()
        if "[mcp_servers.repowire]" in content:
            CONFIG_PATH.write_text(_ensure_repowire_mcp_backend_env(content))
            return True  # already installed
        CONFIG_PATH.write_text(content.rstrip() + "\n" + section)
    else:
        CONFIG_PATH.write_text(section.lstrip())

    return True


def uninstall_mcp() -> bool:
    """Remove repowire MCP server from config.toml."""
    if not CONFIG_PATH.exists():
        return False

    content = CONFIG_PATH.read_text()
    if "[mcp_servers.repowire]" not in content:
        return False

    # Remove the section and its key-value lines
    lines = content.splitlines(keepends=True)
    new_lines: list[str] = []
    in_section = False
    for line in lines:
        if line.strip() == "[mcp_servers.repowire]":
            in_section = True
            continue
        if in_section and (line.startswith("[") or not line.strip()):
            if line.startswith("["):
                in_section = False
                new_lines.append(line)
            continue
        if not in_section:
            new_lines.append(line)

    CONFIG_PATH.write_text("".join(new_lines).strip() + "\n" if new_lines else "")
    return True


def check_hooks_installed() -> bool:
    """Check if repowire hooks are configured in Codex."""
    data = _load_hooks()
    hooks = data.get("hooks", {})
    return "Stop" in hooks or "SessionStart" in hooks


def check_mcp_installed() -> bool:
    """Check if repowire MCP server is configured in Codex."""
    if not CONFIG_PATH.exists():
        return False
    return "[mcp_servers.repowire]" in CONFIG_PATH.read_text()


def get_codex_version() -> tuple[int, ...] | None:
    """Get Codex CLI version as a tuple, or None if not installed."""
    try:
        result = subprocess.run(
            ["codex", "--version"], capture_output=True, text=True, timeout=5,
        )
        if result.returncode != 0:
            return None
        # Output format: "codex-cli 0.111.0"
        parts = result.stdout.strip().split()
        version_str = parts[1] if len(parts) >= 2 else parts[0]
        return tuple(int(x) for x in version_str.split("."))
    except (FileNotFoundError, subprocess.TimeoutExpired, ValueError, IndexError):
        return None
