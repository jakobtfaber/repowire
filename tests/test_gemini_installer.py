"""Filesystem install/uninstall tests for the Gemini installer.

Gemini stores hooks and MCP servers together in ~/.gemini/settings.json
(JSON). Writes go through an atomic .tmp + chmod 600 + replace dance.

Tests rebind GEMINI_HOME / SETTINGS_PATH to tmp_path so the real
filesystem is never touched.
"""

from __future__ import annotations

import json

from repowire.installers import gemini as gemini_mod


def _retarget(tmp_path, monkeypatch):
    home = tmp_path / ".gemini"
    monkeypatch.setattr(gemini_mod, "GEMINI_HOME", home)
    monkeypatch.setattr(gemini_mod, "SETTINGS_PATH", home / "settings.json")
    return home


def _read(home):
    return json.loads((home / "settings.json").read_text())


def test_install_hooks_writes_with_600_perms(tmp_path, monkeypatch):
    home = _retarget(tmp_path, monkeypatch)
    gemini_mod.install_hooks()
    # Atomic write should leave the file at 0o600.
    mode = (home / "settings.json").stat().st_mode & 0o777
    assert mode == 0o600


# -- install_mcp ------------------------------------------------------------


def test_install_mcp_on_empty(tmp_path, monkeypatch):
    home = _retarget(tmp_path, monkeypatch)
    assert gemini_mod.install_mcp() is True
    data = _read(home)
    assert data["mcpServers"]["repowire"] == {"command": "repowire", "args": ["mcp"]}


def test_install_mcp_preserves_other_servers_and_keys(tmp_path, monkeypatch):
    home = _retarget(tmp_path, monkeypatch)
    home.mkdir(parents=True)
    pre = {
        "theme": "dark",
        "mcpServers": {"other": {"command": "other", "args": []}},
    }
    (home / "settings.json").write_text(json.dumps(pre))

    gemini_mod.install_mcp()
    data = _read(home)
    assert data["theme"] == "dark"
    assert data["mcpServers"]["other"] == {"command": "other", "args": []}
    assert "repowire" in data["mcpServers"]


def test_install_mcp_is_idempotent(tmp_path, monkeypatch):
    home = _retarget(tmp_path, monkeypatch)
    gemini_mod.install_mcp()
    first = (home / "settings.json").read_text()
    gemini_mod.install_mcp()
    second = (home / "settings.json").read_text()
    assert first == second


# -- uninstall_mcp ----------------------------------------------------------


def test_uninstall_mcp_after_install(tmp_path, monkeypatch):
    home = _retarget(tmp_path, monkeypatch)
    gemini_mod.install_mcp()
    assert gemini_mod.uninstall_mcp() is True
    data = _read(home)
    assert "mcpServers" not in data


def test_uninstall_mcp_keeps_other_servers(tmp_path, monkeypatch):
    home = _retarget(tmp_path, monkeypatch)
    home.mkdir(parents=True)
    pre = {"mcpServers": {"other": {"command": "other"}}}
    (home / "settings.json").write_text(json.dumps(pre))
    gemini_mod.install_mcp()

    assert gemini_mod.uninstall_mcp() is True
    data = _read(home)
    assert data["mcpServers"] == {"other": {"command": "other"}}


def test_uninstall_mcp_noop_when_absent(tmp_path, monkeypatch):
    _retarget(tmp_path, monkeypatch)
    assert gemini_mod.uninstall_mcp() is False


def test_uninstall_mcp_noop_when_no_repowire_entry(tmp_path, monkeypatch):
    home = _retarget(tmp_path, monkeypatch)
    home.mkdir(parents=True)
    pre = {"mcpServers": {"other": {"command": "other"}}}
    (home / "settings.json").write_text(json.dumps(pre))
    assert gemini_mod.uninstall_mcp() is False


# -- Round-trip -------------------------------------------------------------


def test_full_roundtrip_restores_user_config(tmp_path, monkeypatch):
    home = _retarget(tmp_path, monkeypatch)
    home.mkdir(parents=True)
    user_entry = {"hooks": [{"type": "command", "command": "/usr/local/bin/my-hook"}]}
    original = {
        "theme": "dark",
        "hooks": {"AfterAgent": [user_entry]},
        "mcpServers": {"other": {"command": "other"}},
    }
    (home / "settings.json").write_text(json.dumps(original))

    gemini_mod.install_hooks()
    gemini_mod.install_mcp()
    gemini_mod.uninstall_hooks()
    gemini_mod.uninstall_mcp()

    data = _read(home)
    assert data["theme"] == "dark"
    assert data["hooks"]["AfterAgent"] == [user_entry]
    assert data["mcpServers"] == {"other": {"command": "other"}}


# -- check_* ----------------------------------------------------------------


def test_check_mcp_installed_reflects_state(tmp_path, monkeypatch):
    _retarget(tmp_path, monkeypatch)
    assert gemini_mod.check_mcp_installed() is False
    gemini_mod.install_mcp()
    assert gemini_mod.check_mcp_installed() is True
    gemini_mod.uninstall_mcp()
    assert gemini_mod.check_mcp_installed() is False
