"""Filesystem install/uninstall tests for the Claude Code installer.

Claude Code stores hooks in ~/.claude/settings.json (JSON) and the
experimental channel MCP server in ~/.claude.json (JSON).

Unlike codex/gemini, install_hooks replaces the whole event entry —
it does not dedupe a list of repowire+user entries for the same event.
We test the documented behavior, not a hypothetical one.

install_channel is gated by bun + version + server file presence — those
are monkeypatched to keep tests hermetic.
"""

from __future__ import annotations

import json
import shlex
import sys
from pathlib import Path

import pytest

from repowire.config.models import Config as RealConfig
from repowire.installers import claude_code as cc_mod


def _retarget(tmp_path, monkeypatch, *, config=None):
    settings = tmp_path / ".claude" / "settings.json"
    claude_json = tmp_path / ".claude.json"
    monkeypatch.setattr(cc_mod, "CLAUDE_SETTINGS", settings)
    monkeypatch.setattr(cc_mod, "CLAUDE_JSON", claude_json)

    cfg = config or RealConfig()
    monkeypatch.setattr(cc_mod, "load_config", lambda: cfg)
    return settings, claude_json


def _read(path: Path) -> dict:
    return json.loads(path.read_text())


def _write_dispatcher(tmp_path: Path, manifest: dict) -> str:
    script = tmp_path / "hook dispatcher.py"
    script.write_text(
        f"#!{sys.executable}\n"
        "import json, sys\n"
        f"manifest = {manifest!r}\n"
        "if sys.argv[1] != '--manifest-json':\n"
        "    raise SystemExit(2)\n"
        "print(json.dumps(manifest))\n"
    )
    script.chmod(0o755)
    return shlex.quote(str(script))


def _write_dispatcher_program(tmp_path: Path, body: str) -> str:
    script = tmp_path / "failing hook dispatcher.py"
    script.write_text(f"#!{sys.executable}\n{body}")
    script.chmod(0o755)
    return shlex.quote(str(script))


def _dispatcher_settings(command: str) -> dict:
    return {
        "hooks": {
            event: [{
                "hooks": [{"type": "command", "command": f"{command} {event}"}],
            }]
            for event in cc_mod._REQUIRED_HOOK_EVENTS
        },
    }


def _repowire_dispatch_manifest() -> dict:
    return {
        "schema_version": 1,
        "hooks": {
            "Stop": ["repowire hook stop"],
            "StopFailure": ["repowire hook stop"],
            "SessionStart": ["repowire hook session"],
            "SessionEnd": ["repowire hook session"],
            "UserPromptSubmit": ["repowire hook prompt"],
            "Notification": ["repowire hook notification"],
        },
    }


def _repowire_channel_manifest() -> dict:
    return {
        "schema_version": 1,
        "hooks": {
            "Stop": ["repowire hook stop"],
            "StopFailure": ["repowire hook stop"],
        },
    }


# -- install_hooks ----------------------------------------------------------


def test_install_hooks_on_empty_writes_all_events(tmp_path, monkeypatch):
    settings, _ = _retarget(tmp_path, monkeypatch)

    assert cc_mod.install_hooks() is True
    data = _read(settings)

    assert set(data["hooks"]) == {
        "Stop", "StopFailure", "SessionStart", "SessionEnd",
        "UserPromptSubmit", "Notification",
    }
    # All point at the repowire CLI.
    assert "repowire hook stop" in data["hooks"]["Stop"][0]["hooks"][0]["command"]
    assert "repowire hook stop" in data["hooks"]["StopFailure"][0]["hooks"][0]["command"]
    assert "repowire hook session" in data["hooks"]["SessionStart"][0]["hooks"][0]["command"]
    assert "repowire hook prompt" in data["hooks"]["UserPromptSubmit"][0]["hooks"][0]["command"]
    # Notification carries the idle_prompt matcher.
    assert data["hooks"]["Notification"][0]["matcher"] == "idle_prompt"


def test_install_hooks_channel_mode_only_writes_stop(tmp_path, monkeypatch):
    settings, _ = _retarget(tmp_path, monkeypatch)
    assert cc_mod.install_hooks(channel_mode=True) is True
    data = _read(settings)
    assert set(data["hooks"]) == {"Stop", "StopFailure"}


def test_install_hooks_channel_mode_noops_for_filtered_external_manifest(
    tmp_path, monkeypatch,
):
    settings, _ = _retarget(tmp_path, monkeypatch)
    command = _write_dispatcher(tmp_path, _repowire_channel_manifest())
    original = _dispatcher_settings(command)
    settings.parent.mkdir(parents=True)
    settings.write_text(json.dumps(original))

    assert cc_mod.install_hooks(channel_mode=True) is True
    assert _read(settings) == original


def test_install_hooks_channel_mode_rejects_full_external_manifest(
    tmp_path, monkeypatch,
):
    settings, _ = _retarget(tmp_path, monkeypatch)
    command = _write_dispatcher(tmp_path, _repowire_dispatch_manifest())
    original = _dispatcher_settings(command)
    settings.parent.mkdir(parents=True)
    settings.write_text(json.dumps(original))

    with pytest.raises(RuntimeError, match="full Repowire lifecycle"):
        cc_mod.install_hooks(channel_mode=True)
    assert _read(settings) == original


def test_install_hooks_preserves_top_level_keys(tmp_path, monkeypatch):
    settings, _ = _retarget(tmp_path, monkeypatch)
    settings.parent.mkdir(parents=True)
    settings.write_text(json.dumps({"theme": "dark", "permissions": {"allow": ["Bash"]}}))

    cc_mod.install_hooks()
    data = _read(settings)
    assert data["theme"] == "dark"
    assert data["permissions"] == {"allow": ["Bash"]}
    assert "Stop" in data["hooks"]


def test_install_hooks_is_idempotent(tmp_path, monkeypatch):
    settings, _ = _retarget(tmp_path, monkeypatch)
    cc_mod.install_hooks()
    first = settings.read_text()
    cc_mod.install_hooks()
    second = settings.read_text()
    assert first == second
    # And each (default-on) event has exactly one entry. PreToolUse is opt-in
    # and absent here, so iterate the required set.
    data = json.loads(second)
    for event in cc_mod._REQUIRED_HOOK_EVENTS:
        assert len(data["hooks"][event]) == 1


def test_install_hooks_replaces_existing_repowire_entry_for_same_event(tmp_path, monkeypatch):
    """Documented behavior: install_hooks unconditionally sets each event,
    so a stale repowire entry from an earlier version gets refreshed."""
    settings, _ = _retarget(tmp_path, monkeypatch)
    settings.parent.mkdir(parents=True)
    pre = {"hooks": {"Stop": [{"hooks": [{"type": "command", "command": "old-repowire-hook"}]}]}}
    settings.write_text(json.dumps(pre))

    cc_mod.install_hooks()
    data = _read(settings)
    stop_cmds = [e["hooks"][0]["command"] for e in data["hooks"]["Stop"]]
    assert stop_cmds == ["repowire hook stop"]


def test_install_hooks_noops_when_external_dispatcher_provides_full_set(
    tmp_path, monkeypatch,
):
    settings, _ = _retarget(tmp_path, monkeypatch)
    command = _write_dispatcher(tmp_path, _repowire_dispatch_manifest())
    original = {"theme": "dark", **_dispatcher_settings(command)}
    settings.parent.mkdir(parents=True)
    settings.write_text(json.dumps(original))

    assert cc_mod.install_hooks() is True
    assert _read(settings) == original


# -- PreToolUse remote approval (opt-in) ------------------------------------


def _approval_config(enabled: bool, *, gated=None):
    cfg = RealConfig()
    cfg.experiments.remote_tool_approval.enabled = enabled
    if gated is not None:
        cfg.experiments.remote_tool_approval.gated_tools = gated
    return cfg


def test_pretooluse_absent_when_approval_disabled(tmp_path, monkeypatch):
    settings, _ = _retarget(tmp_path, monkeypatch, config=_approval_config(False))
    cc_mod.install_hooks()
    data = _read(settings)
    assert "PreToolUse" not in data["hooks"]


def test_pretooluse_registered_when_approval_enabled(tmp_path, monkeypatch):
    settings, _ = _retarget(
        tmp_path, monkeypatch, config=_approval_config(True, gated=["Bash", "Edit"]),
    )
    cc_mod.install_hooks()
    entry = _read(settings)["hooks"]["PreToolUse"][0]
    assert entry["matcher"] == "Bash|Edit"
    hook = entry["hooks"][0]
    assert hook["command"] == "repowire hook pretooluse"
    assert hook["timeout"] == cc_mod.PRETOOLUSE_HOOK_TIMEOUT_SECONDS


def test_pretooluse_stripped_when_toggled_off(tmp_path, monkeypatch):
    settings, _ = _retarget(
        tmp_path, monkeypatch, config=_approval_config(True, gated=["Bash"]),
    )
    cc_mod.install_hooks()
    assert "PreToolUse" in _read(settings)["hooks"]

    # Re-install with the experiment off: the prior repowire PreToolUse entry
    # is cleaned up rather than left dangling.
    _retarget(tmp_path, monkeypatch, config=_approval_config(False))
    cc_mod.install_hooks()
    assert "PreToolUse" not in _read(settings)["hooks"]


def test_pretooluse_absent_in_channel_mode(tmp_path, monkeypatch):
    settings, _ = _retarget(
        tmp_path, monkeypatch, config=_approval_config(True, gated=["Bash"]),
    )
    cc_mod.install_hooks(channel_mode=True)
    assert "PreToolUse" not in _read(settings).get("hooks", {})


# -- uninstall_hooks --------------------------------------------------------


def test_uninstall_hooks_after_install_clears_all(tmp_path, monkeypatch):
    settings, _ = _retarget(tmp_path, monkeypatch)
    cc_mod.install_hooks()
    assert cc_mod.uninstall_hooks() is True
    data = _read(settings)
    assert "hooks" not in data


def test_uninstall_hooks_preserves_other_top_level_keys(tmp_path, monkeypatch):
    settings, _ = _retarget(tmp_path, monkeypatch)
    settings.parent.mkdir(parents=True)
    settings.write_text(json.dumps({"theme": "dark"}))
    cc_mod.install_hooks()

    cc_mod.uninstall_hooks()
    data = _read(settings)
    assert data == {"theme": "dark"}


def test_uninstall_hooks_keeps_non_repowire_event_keys(tmp_path, monkeypatch):
    """uninstall_hooks only drops events in HOOK_EVENTS; unrelated event
    names left by the user must survive."""
    settings, _ = _retarget(tmp_path, monkeypatch)
    cc_mod.install_hooks()
    data = _read(settings)
    custom = [{"hooks": [{"type": "command", "command": "custom"}]}]
    data["hooks"]["CustomEvent"] = custom
    settings.write_text(json.dumps(data))

    cc_mod.uninstall_hooks()
    after = _read(settings)
    assert after["hooks"] == {"CustomEvent": custom}


def test_uninstall_hooks_noop_when_absent(tmp_path, monkeypatch):
    _retarget(tmp_path, monkeypatch)
    assert cc_mod.uninstall_hooks() is False


def test_uninstall_hooks_noop_when_no_hooks_key(tmp_path, monkeypatch):
    settings, _ = _retarget(tmp_path, monkeypatch)
    settings.parent.mkdir(parents=True)
    settings.write_text(json.dumps({"theme": "dark"}))
    assert cc_mod.uninstall_hooks() is False


def test_uninstall_hooks_preserves_external_dispatcher_and_removes_direct_hook(
    tmp_path, monkeypatch,
):
    settings, _ = _retarget(tmp_path, monkeypatch)
    command = _write_dispatcher(tmp_path, _repowire_dispatch_manifest())
    direct = {"hooks": [{"type": "command", "command": "repowire hook stop"}]}
    original = _dispatcher_settings(command)
    dispatcher = original["hooks"]["Stop"][0]
    original["hooks"]["Stop"].append(direct)
    settings.parent.mkdir(parents=True)
    settings.write_text(json.dumps(original))

    assert cc_mod.uninstall_hooks() is True
    assert _read(settings)["hooks"]["Stop"] == [dispatcher]


def test_uninstall_hooks_preserves_external_dispatcher_when_probe_fails(
    tmp_path, monkeypatch,
):
    settings, _ = _retarget(tmp_path, monkeypatch)
    command = _write_dispatcher_program(tmp_path, "raise SystemExit(3)\n")
    original = _dispatcher_settings(command)
    dispatcher = original["hooks"]["Stop"][0]
    settings.parent.mkdir(parents=True)
    settings.write_text(json.dumps(original))

    assert cc_mod.uninstall_hooks() is False
    assert _read(settings)["hooks"]["Stop"] == [dispatcher]


# -- check_hooks_installed --------------------------------------------------


def test_check_hooks_installed_requires_full_event_set(tmp_path, monkeypatch):
    _retarget(tmp_path, monkeypatch)
    assert cc_mod.check_hooks_installed() is False
    cc_mod.install_hooks()
    assert cc_mod.check_hooks_installed() is True
    # Channel mode installs only Stop -> not a "full" install per the check.
    cc_mod.uninstall_hooks()
    cc_mod.install_hooks(channel_mode=True)
    assert cc_mod.check_hooks_installed() is False
    assert cc_mod.check_hooks_installed(channel_mode=True) is True


def test_check_configured_hooks_installed_uses_channel_mode(tmp_path, monkeypatch):
    _retarget(tmp_path, monkeypatch)
    cc_mod.install_hooks(channel_mode=True)
    monkeypatch.setattr(cc_mod, "check_channel_installed", lambda: True)

    assert cc_mod.check_configured_hooks_installed() is True


def test_check_hooks_installed_accepts_external_dispatcher_manifest(tmp_path, monkeypatch):
    settings, _ = _retarget(tmp_path, monkeypatch)
    command = _write_dispatcher(tmp_path, _repowire_dispatch_manifest())
    settings.parent.mkdir(parents=True)
    settings.write_text(json.dumps(_dispatcher_settings(command)))

    assert cc_mod.check_hooks_installed() is True


def test_external_dispatcher_is_probed_once_without_running_normal_events(
    tmp_path, monkeypatch,
):
    settings, _ = _retarget(tmp_path, monkeypatch)
    calls = tmp_path / "probe-calls.jsonl"
    normal_event = tmp_path / "normal-event-ran"
    manifest = _repowire_dispatch_manifest()
    script = tmp_path / "recording dispatcher.py"
    script.write_text(
        f"#!{sys.executable}\n"
        "import json, sys\n"
        f"calls = {str(calls)!r}\n"
        f"normal_event = {str(normal_event)!r}\n"
        "with open(calls, 'a') as stream:\n"
        "    stream.write(json.dumps(sys.argv[1:]) + '\\n')\n"
        "if sys.argv[1:] != ['--manifest-json']:\n"
        "    open(normal_event, 'w').close()\n"
        "    raise SystemExit(2)\n"
        f"print(json.dumps({manifest!r}))\n"
    )
    script.chmod(0o755)
    command = shlex.quote(str(script))
    settings.parent.mkdir(parents=True)
    settings.write_text(json.dumps(_dispatcher_settings(command)))

    assert cc_mod.check_hooks_installed() is True
    assert calls.read_text().splitlines() == ['["--manifest-json"]']
    assert not normal_event.exists()


def test_external_dispatcher_probe_supports_interpreter_wrapped_command(
    tmp_path, monkeypatch,
):
    settings, _ = _retarget(tmp_path, monkeypatch)
    script = tmp_path / "interpreter-wrapped-dispatcher.py"
    script.write_text(
        "import json, sys\n"
        "if sys.argv[1:] != ['--manifest-json']:\n"
        "    raise SystemExit(2)\n"
        f"print(json.dumps({_repowire_dispatch_manifest()!r}))\n"
    )
    command = f"/usr/bin/python3 {shlex.quote(str(script))}"
    settings.parent.mkdir(parents=True)
    settings.write_text(json.dumps(_dispatcher_settings(command)))

    assert cc_mod.check_hooks_installed() is True


def test_external_dispatcher_probe_does_not_invoke_a_shell(tmp_path, monkeypatch):
    settings, _ = _retarget(tmp_path, monkeypatch)
    marker = tmp_path / "shell-was-used"
    command = _write_dispatcher_program(
        tmp_path,
        "import json, sys\n"
        "if sys.argv[-1] != '--manifest-json':\n"
        "    raise SystemExit(2)\n"
        f"print(json.dumps({_repowire_dispatch_manifest()!r}))\n",
    )
    command += f" ; touch {shlex.quote(str(marker))}"
    settings.parent.mkdir(parents=True)
    settings.write_text(json.dumps(_dispatcher_settings(command)))

    assert cc_mod.check_hooks_installed() is True
    assert not marker.exists()


def test_check_hooks_installed_does_not_probe_arbitrary_hook(tmp_path, monkeypatch):
    settings, _ = _retarget(tmp_path, monkeypatch)
    marker = tmp_path / "arbitrary-hook-ran"
    script = tmp_path / "ordinary-hook"
    script.write_text(f"#!{sys.executable}\nopen({str(marker)!r}, 'w').close()\n")
    script.chmod(0o755)
    settings.parent.mkdir(parents=True)
    settings.write_text(json.dumps({
        "hooks": {
            "Stop": [{
                "hooks": [{"type": "command", "command": shlex.quote(str(script))}],
            }],
        },
    }))

    assert cc_mod.check_hooks_installed() is False
    assert not marker.exists()


@pytest.mark.parametrize(
    "program",
    [
        "raise SystemExit(3)\n",
        "print('not json')\n",
        "print('{\"schema_version\": 2, \"hooks\": {}}')\n",
        "print('{\"schema_version\": 1, \"hooks\": []}')\n",
        "print('{\"schema_version\": 1, "
        "\"hooks\": {\"Stop\": [\"repowire hook stop\"]}}')\n",
        "import time\ntime.sleep(0.1)\n",
    ],
    ids=["nonzero", "malformed-json", "bad-version", "bad-hooks", "missing-events", "timeout"],
)
def test_check_hooks_installed_fails_closed_for_bad_external_manifest(
    tmp_path, monkeypatch, program,
):
    settings, _ = _retarget(tmp_path, monkeypatch)
    monkeypatch.setattr(cc_mod, "HOOK_MANIFEST_TIMEOUT_SECONDS", 0.01)
    command = _write_dispatcher_program(tmp_path, program)
    settings.parent.mkdir(parents=True)
    settings.write_text(json.dumps(_dispatcher_settings(command)))

    assert cc_mod.check_hooks_installed() is False


# -- install_channel (MCP path for Claude Code) -----------------------------


def _stub_channel_prereqs(monkeypatch, tmp_path):
    """Make install_channel believe bun, version, and server.ts all exist."""
    monkeypatch.setattr(cc_mod, "_has_bun", lambda: True)
    monkeypatch.setattr(cc_mod, "supports_channels", lambda: True)
    fake_server = tmp_path / "fake_pkg" / "channel" / "server.ts"
    fake_server.parent.mkdir(parents=True)
    fake_server.write_text("// stub")
    (fake_server.parent / "package.json").write_text(json.dumps({
        "name": "repowire-channel",
        "dependencies": {
            "@modelcontextprotocol/sdk": "^1.12.0",
            "ws": "^8.18.0",
            "zod": "^3.24.0",
        },
    }))
    for dependency in ("@modelcontextprotocol/sdk", "ws", "zod"):
        (fake_server.parent / "node_modules" / dependency).mkdir(parents=True)
    monkeypatch.setattr(cc_mod, "_find_channel_server", lambda: fake_server)
    monkeypatch.setattr(
        cc_mod.shutil,
        "which",
        lambda command: "/opt/homebrew/bin/bun" if command == "bun" else None,
    )
    # bun install runs in cwd of server.ts; stub it out.
    import subprocess
    def fake_run(*_args, **_kwargs):
        class R:
            returncode = 0
            stderr = b""
        return R()
    monkeypatch.setattr(subprocess, "run", fake_run)
    return fake_server


def test_install_channel_on_empty_writes_mcp_entry(tmp_path, monkeypatch):
    _, claude_json = _retarget(tmp_path, monkeypatch)
    server = _stub_channel_prereqs(monkeypatch, tmp_path)

    ok, msg = cc_mod.install_channel()
    assert ok is True
    assert "Channel transport installed" in msg

    data = _read(claude_json)
    assert data["mcpServers"]["repowire-channel"] == {
        "command": "bun",
        "args": [str(server)],
    }


def test_install_channel_passes_daemon_auth_token_to_channel_env(tmp_path, monkeypatch):
    _, claude_json = _retarget(tmp_path, monkeypatch)
    server = _stub_channel_prereqs(monkeypatch, tmp_path)

    class Daemon:
        auth_token = "secret-token"

    class Config:
        daemon = Daemon()

    monkeypatch.setattr(cc_mod, "load_config", lambda: Config())

    ok, _msg = cc_mod.install_channel()
    assert ok is True

    data = _read(claude_json)
    assert data["mcpServers"]["repowire-channel"] == {
        "command": "bun",
        "args": [str(server)],
        "env": {"REPOWIRE_AUTH_TOKEN": "secret-token"},
    }
    assert cc_mod.check_channel_installed() is True


def test_install_channel_preserves_existing_mcp_servers(tmp_path, monkeypatch):
    _, claude_json = _retarget(tmp_path, monkeypatch)
    claude_json.write_text(json.dumps({
        "theme": "dark",
        "mcpServers": {"other": {"command": "other"}},
    }))
    _stub_channel_prereqs(monkeypatch, tmp_path)

    ok, _msg = cc_mod.install_channel()
    assert ok is True
    data = _read(claude_json)
    assert data["theme"] == "dark"
    assert data["mcpServers"]["other"] == {"command": "other"}
    assert "repowire-channel" in data["mcpServers"]


def test_install_channel_is_idempotent(tmp_path, monkeypatch):
    _, claude_json = _retarget(tmp_path, monkeypatch)
    _stub_channel_prereqs(monkeypatch, tmp_path)

    cc_mod.install_channel()
    first = claude_json.read_text()
    cc_mod.install_channel()
    second = claude_json.read_text()
    assert first == second
    data = json.loads(second)
    # Exactly one repowire-channel entry.
    assert list(data["mcpServers"].keys()).count("repowire-channel") == 1


def test_install_channel_bails_without_bun(tmp_path, monkeypatch):
    _retarget(tmp_path, monkeypatch)
    monkeypatch.setattr(cc_mod, "_has_bun", lambda: False)
    ok, msg = cc_mod.install_channel()
    assert ok is False
    assert "bun" in msg.lower()


def test_install_channel_bails_on_unsupported_version(tmp_path, monkeypatch):
    _retarget(tmp_path, monkeypatch)
    monkeypatch.setattr(cc_mod, "_has_bun", lambda: True)
    monkeypatch.setattr(cc_mod, "supports_channels", lambda: False)
    monkeypatch.setattr(cc_mod, "get_claude_version", lambda: (2, 0, 0))
    ok, msg = cc_mod.install_channel()
    assert ok is False
    assert "channels" in msg.lower()


# -- uninstall_channel ------------------------------------------------------


def test_uninstall_channel_removes_entry(tmp_path, monkeypatch):
    _, claude_json = _retarget(tmp_path, monkeypatch)
    _stub_channel_prereqs(monkeypatch, tmp_path)
    cc_mod.install_channel()

    assert cc_mod.uninstall_channel() is True
    # mcpServers had only repowire-channel -> dropped entirely.
    data = _read(claude_json)
    assert "mcpServers" not in data


def test_uninstall_channel_keeps_other_servers(tmp_path, monkeypatch):
    _, claude_json = _retarget(tmp_path, monkeypatch)
    claude_json.write_text(json.dumps({"mcpServers": {"other": {"command": "other"}}}))
    _stub_channel_prereqs(monkeypatch, tmp_path)
    cc_mod.install_channel()

    assert cc_mod.uninstall_channel() is True
    data = _read(claude_json)
    assert data["mcpServers"] == {"other": {"command": "other"}}


def test_uninstall_channel_noop_when_absent(tmp_path, monkeypatch):
    _retarget(tmp_path, monkeypatch)
    assert cc_mod.uninstall_channel() is False


def test_uninstall_channel_noop_when_not_installed(tmp_path, monkeypatch):
    _, claude_json = _retarget(tmp_path, monkeypatch)
    claude_json.write_text(json.dumps({"mcpServers": {"other": {"command": "other"}}}))
    assert cc_mod.uninstall_channel() is False


def test_check_channel_installed_reflects_state(tmp_path, monkeypatch):
    _retarget(tmp_path, monkeypatch)
    _stub_channel_prereqs(monkeypatch, tmp_path)
    assert cc_mod.check_channel_installed() is False
    cc_mod.install_channel()
    assert cc_mod.check_channel_installed() is True
    cc_mod.uninstall_channel()
    assert cc_mod.check_channel_installed() is False


@pytest.mark.parametrize(
    ("entry_update", "environment_update"),
    [
        ({"command": "node"}, {}),
        ({"args": []}, {}),
        ({"args": ["/stale/server.ts"]}, {}),
        ({}, {"bun": None}),
        ({}, {"missing_dependency": "zod"}),
    ],
    ids=["wrong-command", "missing-args", "stale-path", "missing-bun", "missing-deps"],
)
def test_check_channel_installed_rejects_stale_or_incomplete_entry(
    tmp_path, monkeypatch, entry_update, environment_update,
):
    _, claude_json = _retarget(tmp_path, monkeypatch)
    server = _stub_channel_prereqs(monkeypatch, tmp_path)
    entry = {"command": "bun", "args": [str(server)]}
    entry.update(entry_update)
    if "bun" in environment_update:
        monkeypatch.setattr(cc_mod.shutil, "which", lambda _command: None)
    missing_dependency = environment_update.get("missing_dependency")
    if missing_dependency:
        dependency = server.parent / "node_modules" / missing_dependency
        dependency.rmdir()
    claude_json.write_text(json.dumps({
        "mcpServers": {"repowire-channel": entry},
    }))

    assert cc_mod.check_channel_installed() is False


def test_check_channel_installed_rejects_non_object_entry(tmp_path, monkeypatch):
    _, claude_json = _retarget(tmp_path, monkeypatch)
    _stub_channel_prereqs(monkeypatch, tmp_path)
    claude_json.write_text(json.dumps({
        "mcpServers": {"repowire-channel": "bun"},
    }))

    assert cc_mod.check_channel_installed() is False


def test_check_channel_installed_rejects_bun_symlink_to_other_executable(
    tmp_path, monkeypatch,
):
    _, claude_json = _retarget(tmp_path, monkeypatch)
    server = _stub_channel_prereqs(monkeypatch, tmp_path)
    fake_bun = tmp_path / "bun"
    fake_bun.symlink_to("/usr/bin/env")
    monkeypatch.setattr(cc_mod.shutil, "which", lambda _command: str(fake_bun))
    claude_json.write_text(json.dumps({
        "mcpServers": {
            "repowire-channel": {"command": "bun", "args": [str(server)]},
        },
    }))

    assert cc_mod.check_channel_installed() is False


@pytest.mark.parametrize(
    "package",
    [
        {"name": "other-channel", "dependencies": {
            "@modelcontextprotocol/sdk": "1", "ws": "1", "zod": "1",
        }},
        {"name": "repowire-channel", "dependencies": {
            "@modelcontextprotocol/sdk": "1", "ws": "1",
        }},
        {"name": "repowire-channel", "dependencies": []},
    ],
    ids=["wrong-name", "missing-declaration", "malformed-dependencies"],
)
def test_check_channel_installed_rejects_wrong_package_metadata(
    tmp_path, monkeypatch, package,
):
    _, claude_json = _retarget(tmp_path, monkeypatch)
    server = _stub_channel_prereqs(monkeypatch, tmp_path)
    (server.parent / "package.json").write_text(json.dumps(package))
    claude_json.write_text(json.dumps({
        "mcpServers": {
            "repowire-channel": {"command": "bun", "args": [str(server)]},
        },
    }))

    assert cc_mod.check_channel_installed() is False


@pytest.mark.parametrize(
    "env",
    [None, {}, {"REPOWIRE_AUTH_TOKEN": "wrong"}, "malformed"],
    ids=["missing-env", "missing-token", "mismatched-token", "malformed-env"],
)
def test_check_channel_installed_rejects_bad_auth_env(
    tmp_path, monkeypatch, env,
):
    config = RealConfig()
    config.daemon.auth_token = "expected-token"
    _, claude_json = _retarget(tmp_path, monkeypatch, config=config)
    server = _stub_channel_prereqs(monkeypatch, tmp_path)
    entry = {"command": "bun", "args": [str(server)]}
    if env is not None:
        entry["env"] = env
    claude_json.write_text(json.dumps({
        "mcpServers": {"repowire-channel": entry},
    }))

    assert cc_mod.check_channel_installed() is False


def test_check_channel_installed_rejects_unsupported_claude(tmp_path, monkeypatch):
    _, claude_json = _retarget(tmp_path, monkeypatch)
    server = _stub_channel_prereqs(monkeypatch, tmp_path)
    monkeypatch.setattr(cc_mod, "supports_channels", lambda: False)
    claude_json.write_text(json.dumps({
        "mcpServers": {
            "repowire-channel": {"command": "bun", "args": [str(server)]},
        },
    }))

    assert cc_mod.check_channel_installed() is False


# -- Round-trip -------------------------------------------------------------


def test_hooks_roundtrip_restores_user_settings(tmp_path, monkeypatch):
    settings, _ = _retarget(tmp_path, monkeypatch)
    settings.parent.mkdir(parents=True)
    original = {"theme": "dark", "permissions": {"allow": ["Bash"]}}
    settings.write_text(json.dumps(original))

    cc_mod.install_hooks()
    cc_mod.uninstall_hooks()

    assert _read(settings) == original


def test_channel_roundtrip_restores_user_settings(tmp_path, monkeypatch):
    _, claude_json = _retarget(tmp_path, monkeypatch)
    original = {"theme": "dark", "mcpServers": {"other": {"command": "other"}}}
    claude_json.write_text(json.dumps(original))
    _stub_channel_prereqs(monkeypatch, tmp_path)

    cc_mod.install_channel()
    cc_mod.uninstall_channel()

    assert _read(claude_json) == original
