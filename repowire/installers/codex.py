"""OpenAI Codex CLI installer — hooks and MCP server configuration."""

from __future__ import annotations

import hashlib
import json
import re
import shlex
import subprocess
from pathlib import Path

import tomllib

from repowire.installers.runtime import repowire_console_entrypoint

CODEX_HOME = Path.home() / ".codex"
HOOKS_PATH = CODEX_HOME / "hooks.json"
CONFIG_PATH = CODEX_HOME / "config.toml"
PROFILE_CONFIG_PATH = CODEX_HOME / "repowire.config.toml"

# NOTE: codex has no SessionEnd hook event (hooks/src/events/ upstream:
# session_start, stop, user_prompt_submit, tool/compact/subagent events).
# Quit deregistration for codex rides on the ws-hook agent-pid watcher.
HOOK_EVENTS = ["SessionStart", "Stop", "UserPromptSubmit"]
_MCP_ENV_LINE = 'env = { REPOWIRE_BACKEND = "codex" }'
_AUTO_APPROVE_MCP_TOOLS = (
    "ack",
    "answer",
    "ask",
    "ask_many",
    "ask_many_result",
    "kill_peer",
    "list_peers",
    "notify_peer",
    "orchestrator_status",
    "set_description",
    "spawn_peer",
    "wait_on_ack",
    "whoami",
)
_MCP_TOOL_SECTION_PREFIX = "[mcp_servers.repowire.tools."

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


def _repowire_hooks() -> dict[str, dict]:
    executable = shlex.quote(repowire_console_entrypoint())
    return {
        "SessionStart": _make_hook_entry(
            f"{executable} hook session --backend=codex",
            matcher="startup|resume|clear",
        ),
        "Stop": _make_hook_entry(f"{executable} hook stop --backend=codex"),
        "UserPromptSubmit": _make_hook_entry(
            f"{executable} hook prompt --backend=codex"
        ),
    }


def _profile_config() -> str:
    executable = shlex.quote(repowire_console_entrypoint())
    return (
        '[[hooks.SessionStart]]\n'
        'matcher = "startup|resume|clear"\n\n'
        '[[hooks.SessionStart.hooks]]\n'
        'type = "command"\n'
        f'command = "{executable} hook session --backend=codex"\n\n'
        '[[hooks.UserPromptSubmit]]\n\n'
        '[[hooks.UserPromptSubmit.hooks]]\n'
        'type = "command"\n'
        f'command = "{executable} hook prompt --backend=codex"\n\n'
        '[[hooks.Stop]]\n\n'
        '[[hooks.Stop.hooks]]\n'
        'type = "command"\n'
        f'command = "{executable} hook stop --backend=codex"\n'
    )


_HOOK_GROUP = re.compile(r"^\[\[hooks\.[^.\]]+\]\]$")


def _remove_inline_repowire_hooks(content: str) -> str:
    lines = content.splitlines(keepends=True)
    output: list[str] = []
    index = 0
    while index < len(lines):
        if not _HOOK_GROUP.match(lines[index].strip()):
            output.append(lines[index])
            index += 1
            continue
        end = index + 1
        while end < len(lines):
            stripped = lines[end].strip()
            if _HOOK_GROUP.match(stripped):
                break
            if stripped.startswith("[") and not stripped.startswith("[[hooks."):
                break
            end += 1
        group = lines[index:end]
        handler_starts = [
            offset
            for offset, line in enumerate(group)
            if line.strip().startswith("[[hooks.")
            and line.strip().endswith(".hooks]]")
        ]
        if not handler_starts:
            output.extend(group)
            index = end
            continue
        kept_handlers: list[str] = []
        for position, start in enumerate(handler_starts):
            stop = (
                handler_starts[position + 1]
                if position + 1 < len(handler_starts)
                else len(group)
            )
            handler = group[start:stop]
            if not any(
                "repowire" in line and " hook " in line for line in handler
            ):
                kept_handlers.extend(handler)
        if kept_handlers:
            output.extend(group[:handler_starts[0]])
            output.extend(kept_handlers)
        index = end
    return "".join(output)


def install_profile_hooks() -> bool:
    changed = uninstall_hooks()
    CODEX_HOME.mkdir(parents=True, exist_ok=True)
    current = PROFILE_CONFIG_PATH.read_text() if PROFILE_CONFIG_PATH.exists() else ""
    without_hooks = _remove_inline_repowire_hooks(current).rstrip()
    desired = f"{without_hooks}\n\n" if without_hooks else ""
    desired += _profile_config()
    if current != desired:
        tomllib.loads(desired)
        PROFILE_CONFIG_PATH.write_text(desired)
        changed = True
    if CONFIG_PATH.exists():
        before = CONFIG_PATH.read_text()
        after = _remove_inline_repowire_hooks(before)
        if after != before:
            CONFIG_PATH.write_text(after)
            changed = True
    return changed


def uninstall_profile_hooks() -> bool:
    if not PROFILE_CONFIG_PATH.exists():
        return False
    before = PROFILE_CONFIG_PATH.read_text()
    after = _remove_inline_repowire_hooks(before).strip()
    if after == before.strip():
        return False
    if after:
        tomllib.loads(after)
        PROFILE_CONFIG_PATH.write_text(after + "\n")
    else:
        PROFILE_CONFIG_PATH.unlink()
    return True


def check_profile_hooks_installed() -> bool:
    if not PROFILE_CONFIG_PATH.exists():
        return False
    try:
        hooks = tomllib.loads(PROFILE_CONFIG_PATH.read_text()).get("hooks", {})
    except tomllib.TOMLDecodeError:
        return False
    expected = _repowire_hooks()
    for event, entry in expected.items():
        entries = hooks.get(event, [])
        if not any(
            candidate.get("matcher") == entry.get("matcher")
            and candidate.get("hooks") == entry.get("hooks")
            for candidate in entries
        ):
            return False
    return not any(
        _is_repowire_hook(entry)
        for entries in _load_hooks().get("hooks", {}).values()
        for entry in entries
    )


def install_opt_in() -> bool:
    """Install dormant global Codex integration and remove the obsolete profile."""
    changed = uninstall_profile_hooks()
    if uninstall_mcp(PROFILE_CONFIG_PATH):
        changed = True
    if CONFIG_PATH.exists():
        before = CONFIG_PATH.read_text()
        after = _remove_inline_repowire_hooks(before)
        if after != before:
            CONFIG_PATH.write_text(after)
            changed = True
    if install_inline_hooks():
        changed = True
    install_mcp()
    return changed


def install_inline_hooks() -> bool:
    changed = uninstall_hooks()
    if HOOKS_PATH.exists() and _load_hooks() == {}:
        HOOKS_PATH.unlink()
        changed = True
    CODEX_HOME.mkdir(parents=True, exist_ok=True)
    current = CONFIG_PATH.read_text() if CONFIG_PATH.exists() else ""
    without_hooks = _remove_inline_repowire_hooks(current).rstrip()
    desired = f"{without_hooks}\n\n" if without_hooks else ""
    desired += _profile_config()
    if desired != current:
        tomllib.loads(desired)
        CONFIG_PATH.write_text(desired)
        changed = True
    parsed = tomllib.loads(CONFIG_PATH.read_text())
    content = CONFIG_PATH.read_text()
    for event, expected in _repowire_hooks().items():
        for group_index, entry in enumerate(parsed.get("hooks", {}).get(event, [])):
            if entry.get("hooks") != expected.get("hooks"):
                continue
            command = entry["hooks"][0]["command"]
            key = f"{CONFIG_PATH}:{_EVENT_LABELS[event]}:{group_index}:0"
            content = _upsert_hook_state(
                content,
                key,
                trusted_hash_for(event, command, entry.get("matcher")),
            )
            break
    if content != CONFIG_PATH.read_text():
        CONFIG_PATH.write_text(content)
        changed = True
    return changed


def uninstall_inline_hooks() -> bool:
    if not CONFIG_PATH.exists():
        return False
    before = CONFIG_PATH.read_text()
    after = _remove_inline_repowire_hooks(before)
    if after == before:
        return False
    CONFIG_PATH.write_text(after)
    return True


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


def _reindex_trusted_hashes(
    index_maps: dict[
        str,
        dict[tuple[int, int], tuple[int, int] | None],
    ],
) -> None:
    if not CONFIG_PATH.exists():
        return
    labels = {event: _EVENT_LABELS[event] for event in index_maps}
    output: list[str] = []
    skip_section = False
    for line in CONFIG_PATH.read_text().splitlines(keepends=True):
        stripped = line.strip()
        if stripped.startswith("[") and stripped.endswith("]"):
            skip_section = False
            if stripped.startswith('[hooks.state."') and stripped.endswith('"]'):
                key = stripped[len('[hooks.state."'):-2]
                try:
                    base, group_text, handler_text = key.rsplit(":", 2)
                except ValueError:
                    output.append(line)
                    continue
                for event, label in labels.items():
                    if base != f"{HOOKS_PATH}:{label}":
                        continue
                    try:
                        group = int(group_text)
                        handler = int(handler_text)
                    except ValueError:
                        break
                    mapped = index_maps[event].get(
                        (group, handler), (group, handler)
                    )
                    if mapped is None:
                        skip_section = True
                    elif mapped != (group, handler):
                        new_key = f"{base}:{mapped[0]}:{mapped[1]}"
                        line = line.replace(key, new_key, 1)
                    break
        if not skip_section:
            output.append(line)
    CONFIG_PATH.write_text("".join(output))


def _is_repowire_hook(entry: dict) -> bool:
    """Check if a hook entry belongs to repowire."""
    return any(_is_repowire_handler(handler) for handler in entry.get("hooks", []))


def _is_repowire_handler(handler: dict) -> bool:
    return "repowire" in handler.get("command", "")


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
    index_maps: dict[
        str,
        dict[tuple[int, int], tuple[int, int] | None],
    ] = {}

    for event, entry in _repowire_hooks().items():
        existing = hooks.get(event, [])
        refreshed: list[dict] = []
        installed = False
        event_map: dict[tuple[int, int], tuple[int, int] | None] = {}
        desired_handler = entry["hooks"][0]
        for old_group, current in enumerate(existing):
            handlers = current.get("hooks", [])
            if not any(_is_repowire_handler(handler) for handler in handlers):
                new_group = len(refreshed)
                refreshed.append(current)
                for old_handler in range(len(handlers)):
                    event_map[(old_group, old_handler)] = (
                        new_group,
                        old_handler,
                    )
                continue

            new_group = len(refreshed)
            new_handlers: list[dict] = []
            repowire_count = 0
            for old_handler, handler in enumerate(handlers):
                if _is_repowire_handler(handler):
                    repowire_count += 1
                    if not installed:
                        event_map[(old_group, old_handler)] = (
                            new_group,
                            len(new_handlers),
                        )
                        new_handlers.append(desired_handler)
                        installed = True
                    else:
                        event_map[(old_group, old_handler)] = None
                    continue
                event_map[(old_group, old_handler)] = (
                    new_group,
                    len(new_handlers),
                )
                new_handlers.append(handler)
            if new_handlers:
                if repowire_count == len(handlers):
                    refreshed.append(entry)
                else:
                    mixed = dict(current)
                    mixed["hooks"] = new_handlers
                    refreshed.append(mixed)
        if not installed:
            refreshed.append(entry)
        hooks[event] = refreshed
        index_maps[event] = event_map

    # Drop entries for events codex doesn't support (a prior repowire version
    # wrote an inert SessionEnd group).
    stale = hooks.get("SessionEnd", [])
    if stale:
        kept = [e for e in stale if not _is_repowire_hook(e)]
        if kept:
            hooks["SessionEnd"] = kept
        else:
            del hooks["SessionEnd"]

    _reindex_trusted_hashes(index_maps)
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
    index_maps: dict[str, dict[tuple[int, int], tuple[int, int] | None]] = {}
    for event in HOOK_EVENTS:
        entries = hooks.get(event, [])
        filtered: list[dict] = []
        event_map: dict[tuple[int, int], tuple[int, int] | None] = {}
        for old_group, entry in enumerate(entries):
            kept_handlers: list[dict] = []
            for old_handler, handler in enumerate(entry.get("hooks", [])):
                if _is_repowire_handler(handler):
                    event_map[(old_group, old_handler)] = None
                    removed = True
                    continue
                event_map[(old_group, old_handler)] = (
                    len(filtered),
                    len(kept_handlers),
                )
                kept_handlers.append(handler)
            if kept_handlers:
                kept_entry = dict(entry)
                kept_entry["hooks"] = kept_handlers
                filtered.append(kept_entry)
        index_maps[event] = event_map
        if filtered:
            hooks[event] = filtered
        else:
            hooks.pop(event, None)

    if not hooks:
        data.pop("hooks", None)

    if removed:
        _reindex_trusted_hashes(index_maps)
        _save_hooks(data)
    return removed


def _enable_hooks_feature(content: str) -> str:
    """Enable the hooks feature flag in config.toml.

    Codex hooks default to false. We need features.hooks = true for them to fire.
    Codex 0.129.0 renamed `codex_hooks` to `hooks`. Migrate the legacy key
    away because current Codex releases warn whenever it is present.
    """
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

    return content


def _split_toml_comment(value: str) -> tuple[str, str]:
    quote = ""
    escaped = False
    for index, char in enumerate(value):
        if escaped:
            escaped = False
        elif char == "\\" and quote == '"':
            escaped = True
        elif quote:
            if char == quote:
                quote = ""
        elif char in {"'", '"'}:
            quote = char
        elif char == "#":
            body = value[:index].rstrip()
            return body, value[len(body):]
    return value.rstrip(), value[len(value.rstrip()):]


def _unquoted_delimiter(value: str, delimiter: str) -> int | None:
    quote = ""
    escaped = False
    for index, char in enumerate(value):
        if escaped:
            escaped = False
        elif char == "\\" and quote == '"':
            escaped = True
        elif quote:
            if char == quote:
                quote = ""
        elif char in {"'", '"'}:
            quote = char
        elif char == delimiter:
            return index
    return None


def _upsert_inline_backend(value: str) -> str:
    opening = value.find("{")
    closing = value.rfind("}")
    if opening < 0 or closing < opening:
        return value
    inner = value[opening + 1:closing]
    if not inner.strip():
        return (
            f'{value[:opening + 1]} REPOWIRE_BACKEND = "codex" '
            f"{value[closing:]}"
        )
    entries: list[str] = []
    start = 0
    while start <= len(inner):
        relative = _unquoted_delimiter(inner[start:], ",")
        if relative is None:
            entries.append(inner[start:])
            break
        end = start + relative
        entries.append(inner[start:end])
        start = end + 1

    found = False
    for index, entry in enumerate(entries):
        equals = _unquoted_delimiter(entry, "=")
        if equals is None or entry[:equals].strip() != "REPOWIRE_BACKEND":
            continue
        trailing = entry[len(entry.rstrip()):]
        entries[index] = f'{entry[:equals + 1]} "codex"{trailing}'
        found = True
        break
    if not found:
        trailing = entries[-1][len(entries[-1].rstrip()):]
        entries[-1] = entries[-1].rstrip()
        entries.append(f' REPOWIRE_BACKEND = "codex"{trailing}')
    return f"{value[:opening + 1]}{','.join(entries)}{value[closing:]}"


def _ensure_repowire_mcp_backend_env(content: str, executable: str) -> str:
    lines = content.splitlines()
    out: list[str] = []
    in_section = False
    in_env_section = False
    saw_section = False
    saw_env = False
    saw_command = False
    saw_backend = False

    for line in lines:
        stripped = line.strip()
        if stripped.startswith("[") and stripped.endswith("]"):
            next_is_env = stripped == "[mcp_servers.repowire.env]"
            if in_section:
                if not saw_command:
                    out.append(f"command = {json.dumps(executable)}")
                if not saw_env and not next_is_env:
                    out.append(_MCP_ENV_LINE)
            if in_env_section and not saw_backend:
                out.append('REPOWIRE_BACKEND = "codex"')
            in_section = stripped == "[mcp_servers.repowire]"
            in_env_section = next_is_env
            saw_section = saw_section or in_section
            if in_section:
                saw_env = False
                saw_command = False
            if in_env_section:
                saw_backend = False
            out.append(line)
            continue

        key = stripped.partition("=")[0].strip() if "=" in stripped else ""
        if in_section and key == "env":
            saw_env = True
            value, comment = _split_toml_comment(line.split("=", 1)[1])
            line = (
                f"{line.split('=', 1)[0]}="
                f"{_upsert_inline_backend(value)}{comment}"
            )
        elif (
            in_section
            and key == "command"
        ):
            saw_command = True
            _, comment = _split_toml_comment(line.split("=", 1)[1])
            line = f"{line.split('=', 1)[0]}= {json.dumps(executable)}{comment}"
        elif in_env_section and "=" in stripped:
            if key == "REPOWIRE_BACKEND":
                saw_backend = True
                line = 'REPOWIRE_BACKEND = "codex"'
        out.append(line)

    if in_section:
        if not saw_env:
            out.append(_MCP_ENV_LINE)
        if not saw_command:
            out.append(f"command = {json.dumps(executable)}")
    if in_env_section and not saw_backend:
        out.append('REPOWIRE_BACKEND = "codex"')

    if not saw_section:
        return content
    return "\n".join(out).rstrip() + "\n"


def _ensure_repowire_mcp_tool_approvals(content: str) -> str:
    """Auto-approve the bounded tool set required for mesh orchestration."""
    lines = content.splitlines()
    out: list[str] = []
    present: set[str] = set()
    current_tool: str | None = None
    current_has_mode = False

    def finish_tool_section() -> None:
        if current_tool is not None and not current_has_mode:
            out.append('approval_mode = "approve"')

    for line in lines:
        stripped = line.strip()
        if stripped.startswith("[") and stripped.endswith("]"):
            finish_tool_section()
            current_tool = None
            current_has_mode = False
            if stripped.startswith(_MCP_TOOL_SECTION_PREFIX):
                tool = stripped[len(_MCP_TOOL_SECTION_PREFIX):-1]
                if tool in _AUTO_APPROVE_MCP_TOOLS:
                    present.add(tool)
                    current_tool = tool
        elif current_tool is not None and "=" in stripped:
            key = stripped.partition("=")[0].strip()
            current_has_mode = current_has_mode or key == "approval_mode"
        out.append(line)
    finish_tool_section()

    for tool in _AUTO_APPROVE_MCP_TOOLS:
        if tool in present:
            continue
        out.extend(
            [
                "",
                f"{_MCP_TOOL_SECTION_PREFIX}{tool}]",
                'approval_mode = "approve"',
            ]
        )
    return "\n".join(out).rstrip() + "\n"


def _preflight_repowire_mcp_shape(content: str) -> bool:
    """Reject valid TOML spellings the surgical editor cannot preserve safely.

    The installer intentionally preserves comments and unrelated formatting
    instead of serializing the entire file. Parse first, then only edit the
    canonical table spelling that the line editor understands.
    """
    parsed = tomllib.loads(content)
    servers = parsed.get("mcp_servers", {})
    if not isinstance(servers, dict) or "repowire" not in servers:
        return False

    repowire = servers["repowire"]
    if not isinstance(repowire, dict):
        raise ValueError("unsupported Codex Repowire MCP configuration")

    stripped_lines = [line.strip() for line in content.splitlines()]
    if "[mcp_servers.repowire]" not in stripped_lines:
        raise ValueError(
            "unsupported Codex Repowire MCP table spelling; refusing to rewrite"
        )

    root_start = stripped_lines.index("[mcp_servers.repowire]") + 1
    for stripped in stripped_lines[root_start:]:
        if stripped.startswith("[") and stripped.endswith("]"):
            break
        if "=" not in stripped:
            continue
        key = stripped.partition("=")[0].strip().strip("'\"")
        if key == "tools":
            raise ValueError(
                "inline Codex Repowire tool tables are unsupported; "
                "refusing to rewrite"
            )

    tools = repowire.get("tools", {})
    if not isinstance(tools, dict):
        raise ValueError("unsupported Codex Repowire tools configuration")
    for tool in _AUTO_APPROVE_MCP_TOOLS:
        tool_config = tools.get(tool)
        if tool_config is None:
            continue
        header = f"{_MCP_TOOL_SECTION_PREFIX}{tool}]"
        if header not in stripped_lines or not isinstance(tool_config, dict):
            raise ValueError(
                f"unsupported Codex Repowire tool table for {tool}; "
                "refusing to rewrite"
            )
        if "approval_mode" not in tool_config:
            continue
        section_start = stripped_lines.index(header) + 1
        has_canonical_mode = False
        for stripped in stripped_lines[section_start:]:
            if stripped.startswith("[") and stripped.endswith("]"):
                break
            if "=" in stripped:
                key = stripped.partition("=")[0].strip()
                has_canonical_mode = has_canonical_mode or key == "approval_mode"
        if not has_canonical_mode:
            raise ValueError(
                f"unsupported Codex approval_mode key for {tool}; "
                "refusing to rewrite"
            )
    return True


def install_mcp(config_path: Path | None = None) -> bool:
    """Add repowire MCP server to a Codex config file.

    Appends the [mcp_servers.repowire] section. Preserves existing content.
    Also enables the hooks feature flag (required for hooks to fire).
    """
    CODEX_HOME.mkdir(parents=True, exist_ok=True)
    config_path = config_path or CONFIG_PATH
    content = config_path.read_text() if config_path.exists() else ""
    has_repowire = _preflight_repowire_mcp_shape(content)
    content = _enable_hooks_feature(content)

    executable = repowire_console_entrypoint()
    section = (
        "\n[mcp_servers.repowire]\n"
        f"command = {json.dumps(executable)}\n"
        'args = ["mcp"]\n'
        "enabled = false\n"
        f"{_MCP_ENV_LINE}\n"
    )

    if has_repowire:
        updated = _ensure_repowire_mcp_backend_env(content, executable)
        if updated and not updated.endswith("\n"):
            updated += "\n"
        lines = updated.splitlines(keepends=True)
        output: list[str] = []
        in_root = False
        wrote_enabled = False
        for line in lines:
            stripped = line.strip()
            if stripped.startswith("[") and stripped.endswith("]"):
                if in_root and not wrote_enabled:
                    output.append("enabled = false\n")
                in_root = stripped == "[mcp_servers.repowire]"
                wrote_enabled = False
            if in_root and stripped.partition("=")[0].strip() == "enabled":
                if not wrote_enabled:
                    output.append("enabled = false\n")
                    wrote_enabled = True
                continue
            output.append(line)
        if in_root and not wrote_enabled:
            output.append("enabled = false\n")
        updated = "".join(output)
    else:
        updated = content.rstrip() + "\n" + section
    updated = _ensure_repowire_mcp_tool_approvals(updated)
    tomllib.loads(updated)
    config_path.write_text(updated)

    return True


def _is_repowire_mcp_section(header: str) -> bool:
    return (
        header == "[mcp_servers.repowire]"
        or header.startswith("[mcp_servers.repowire.")
    )


def uninstall_mcp(config_path: Path | None = None) -> bool:
    """Remove the Repowire MCP server and its nested configuration."""
    config_path = config_path or CONFIG_PATH
    if not config_path.exists():
        return False

    content = config_path.read_text()
    if not _preflight_repowire_mcp_shape(content):
        return False

    lines = content.splitlines(keepends=True)
    new_lines: list[str] = []
    in_repowire_section = False
    for line in lines:
        stripped = line.strip()
        if stripped.startswith("[") and stripped.endswith("]"):
            in_repowire_section = _is_repowire_mcp_section(stripped)
            if in_repowire_section:
                continue
        if not in_repowire_section:
            new_lines.append(line)

    updated = "".join(new_lines).strip() + "\n" if new_lines else ""
    tomllib.loads(updated)
    if updated.strip():
        config_path.write_text(updated)
    elif config_path == CONFIG_PATH:
        config_path.write_text("")
    else:
        config_path.unlink()
    return True


def check_hooks_installed() -> bool:
    """Check if repowire hooks are configured in Codex."""
    data = _load_hooks()
    hooks = data.get("hooks", {})
    try:
        expected_hooks = _repowire_hooks()
    except RuntimeError:
        return False
    json_installed = all(
        any(candidate == expected for candidate in hooks.get(event, []))
        for event, expected in expected_hooks.items()
    )
    if json_installed:
        return True
    if not CONFIG_PATH.exists():
        return False
    try:
        inline = tomllib.loads(CONFIG_PATH.read_text()).get("hooks", {})
    except tomllib.TOMLDecodeError:
        return False
    return all(
        any(
            candidate.get("matcher") == expected.get("matcher")
            and candidate.get("hooks") == expected.get("hooks")
            for candidate in inline.get(event, [])
        )
        for event, expected in expected_hooks.items()
    )


def check_mcp_installed(config_path: Path | None = None) -> bool:
    """Check if repowire MCP server is configured in Codex."""
    config_path = config_path or CONFIG_PATH
    if not config_path.exists():
        return False
    try:
        expected = repowire_console_entrypoint()
    except RuntimeError:
        return False
    in_section = False
    for line in config_path.read_text().splitlines():
        stripped = line.strip()
        if stripped.startswith("[") and stripped.endswith("]"):
            in_section = stripped == "[mcp_servers.repowire]"
            continue
        if not in_section or "=" not in stripped:
            continue
        key, value = stripped.split("=", 1)
        if key.strip() != "command":
            continue
        try:
            command, _ = _split_toml_comment(value)
            return json.loads(command.strip()) == expected
        except json.JSONDecodeError:
            return False
    return False


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
