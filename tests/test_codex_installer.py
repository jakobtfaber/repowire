"""Filesystem install/uninstall tests for the Codex installer.

Codex stores hooks in ~/.codex/hooks.json (JSON) and the MCP server +
`[features] hooks = true` flag in ~/.codex/config.toml (string-edited).

Tests rebind the module-level path constants to a tmp_path so the real
filesystem is never touched. install_hooks / install_mcp must preserve
user content, be idempotent, and round-trip cleanly via the matching
uninstall.
"""

from __future__ import annotations

from repowire.installers import codex as codex_mod


def _retarget(tmp_path, monkeypatch):
    """Point codex module's path constants at tmp_path."""
    home = tmp_path / ".codex"
    monkeypatch.setattr(codex_mod, "CODEX_HOME", home)
    monkeypatch.setattr(codex_mod, "HOOKS_PATH", home / "hooks.json")
    monkeypatch.setattr(codex_mod, "CONFIG_PATH", home / "config.toml")
    return home


# -- install_mcp / config.toml ----------------------------------------------


def test_install_mcp_on_empty_writes_section_and_feature_flag(tmp_path, monkeypatch):
    home = _retarget(tmp_path, monkeypatch)

    assert codex_mod.install_mcp() is True
    content = (home / "config.toml").read_text()

    assert "[mcp_servers.repowire]" in content
    assert 'command = "repowire"' in content
    assert 'args = ["mcp"]' in content
    assert 'REPOWIRE_BACKEND = "codex"' in content
    assert "[features]" in content
    assert "hooks = true" in content


def test_install_mcp_preserves_existing_config(tmp_path, monkeypatch):
    home = _retarget(tmp_path, monkeypatch)
    home.mkdir(parents=True)
    pre = (
        "model = \"gpt-5\"\n"
        "\n"
        "[mcp_servers.other]\n"
        "command = \"other-tool\"\n"
        "args = [\"serve\"]\n"
    )
    (home / "config.toml").write_text(pre)

    codex_mod.install_mcp()
    content = (home / "config.toml").read_text()

    # Existing content survives verbatim.
    assert "model = \"gpt-5\"" in content
    assert "[mcp_servers.other]" in content
    assert "command = \"other-tool\"" in content
    # And ours is appended.
    assert "[mcp_servers.repowire]" in content


def test_install_mcp_is_idempotent(tmp_path, monkeypatch):
    home = _retarget(tmp_path, monkeypatch)

    codex_mod.install_mcp()
    first = (home / "config.toml").read_text()
    codex_mod.install_mcp()
    second = (home / "config.toml").read_text()

    assert first == second
    assert second.count("[mcp_servers.repowire]") == 1
    assert second.count("hooks = true") == 1
    assert second.count("REPOWIRE_BACKEND") == 1


def test_install_mcp_upgrades_existing_repowire_section_with_backend_env(
    tmp_path, monkeypatch,
):
    home = _retarget(tmp_path, monkeypatch)
    home.mkdir(parents=True)
    (home / "config.toml").write_text(
        "[mcp_servers.repowire]\n"
        'command = "repowire"\n'
        'args = ["mcp"]\n'
    )

    codex_mod.install_mcp()
    content = (home / "config.toml").read_text()

    assert 'REPOWIRE_BACKEND = "codex"' in content


def test_install_mcp_merges_existing_repowire_env(tmp_path, monkeypatch):
    home = _retarget(tmp_path, monkeypatch)
    home.mkdir(parents=True)
    (home / "config.toml").write_text(
        "[mcp_servers.repowire]\n"
        'command = "repowire"\n'
        'args = ["mcp"]\n'
        'env = { FOO = "bar" }\n'
    )

    codex_mod.install_mcp()
    content = (home / "config.toml").read_text()

    assert 'FOO = "bar"' in content
    assert 'REPOWIRE_BACKEND = "codex"' in content


def test_install_mcp_respects_existing_features_block(tmp_path, monkeypatch):
    home = _retarget(tmp_path, monkeypatch)
    home.mkdir(parents=True)
    (home / "config.toml").write_text("[features]\nhooks = false\n")

    codex_mod.install_mcp()
    content = (home / "config.toml").read_text()
    # Existing hooks flag respected (not overwritten).
    assert "hooks = false" in content
    assert "hooks = true" not in content


def test_install_mcp_injects_into_existing_features_block(tmp_path, monkeypatch):
    home = _retarget(tmp_path, monkeypatch)
    home.mkdir(parents=True)
    (home / "config.toml").write_text("[features]\nother = true\n")

    codex_mod.install_mcp()
    content = (home / "config.toml").read_text()
    assert "hooks = true" in content
    assert "other = true" in content
    # Only one [features] header.
    assert content.count("[features]") == 1


def test_install_mcp_migrates_legacy_codex_hooks_flag(tmp_path, monkeypatch):
    home = _retarget(tmp_path, monkeypatch)
    home.mkdir(parents=True)
    (home / "config.toml").write_text("[features]\ncodex_hooks = true\n")

    codex_mod.install_mcp()
    content = (home / "config.toml").read_text()
    assert "codex_hooks" not in content
    assert "hooks = true" in content


def test_install_mcp_removes_legacy_codex_hooks_when_hooks_exists(tmp_path, monkeypatch):
    home = _retarget(tmp_path, monkeypatch)
    home.mkdir(parents=True)
    (home / "config.toml").write_text("[features]\ncodex_hooks = true\nhooks = false\n")

    codex_mod.install_mcp()
    content = (home / "config.toml").read_text()
    assert "codex_hooks" not in content
    assert "hooks = false" in content
    assert "hooks = true" not in content


# -- uninstall_mcp ----------------------------------------------------------


def test_uninstall_mcp_removes_section(tmp_path, monkeypatch):
    home = _retarget(tmp_path, monkeypatch)

    codex_mod.install_mcp()
    assert codex_mod.uninstall_mcp() is True
    content = (home / "config.toml").read_text()
    assert "[mcp_servers.repowire]" not in content
    assert "command = \"repowire\"" not in content


def test_uninstall_mcp_keeps_other_sections(tmp_path, monkeypatch):
    home = _retarget(tmp_path, monkeypatch)
    home.mkdir(parents=True)
    pre = (
        "model = \"gpt-5\"\n"
        "\n"
        "[mcp_servers.other]\n"
        "command = \"other-tool\"\n"
    )
    (home / "config.toml").write_text(pre)
    codex_mod.install_mcp()

    codex_mod.uninstall_mcp()
    content = (home / "config.toml").read_text()
    assert "[mcp_servers.other]" in content
    assert "command = \"other-tool\"" in content
    assert "model = \"gpt-5\"" in content


def test_uninstall_mcp_noop_when_absent(tmp_path, monkeypatch):
    _retarget(tmp_path, monkeypatch)
    # No config.toml at all.
    assert codex_mod.uninstall_mcp() is False


def test_uninstall_mcp_noop_when_section_missing(tmp_path, monkeypatch):
    home = _retarget(tmp_path, monkeypatch)
    home.mkdir(parents=True)
    (home / "config.toml").write_text("model = \"gpt-5\"\n")
    assert codex_mod.uninstall_mcp() is False


# -- check_* ----------------------------------------------------------------


def test_check_mcp_installed_reflects_state(tmp_path, monkeypatch):
    _retarget(tmp_path, monkeypatch)
    assert codex_mod.check_mcp_installed() is False
    codex_mod.install_mcp()
    assert codex_mod.check_mcp_installed() is True
    codex_mod.uninstall_mcp()
    assert codex_mod.check_mcp_installed() is False


# -- install_hooks / hooks.json + trust hashes -------------------------------


def test_install_hooks_does_not_register_session_end(tmp_path, monkeypatch):
    """Codex has no SessionEnd hook event — an entry would be silently inert.
    Quit deregistration for codex rides on the ws-hook agent-pid watcher."""
    import json

    _retarget(tmp_path, monkeypatch)
    assert codex_mod.install_hooks() is True
    hooks = json.loads(codex_mod.HOOKS_PATH.read_text())["hooks"]
    assert "SessionEnd" not in hooks


def test_install_hooks_removes_stale_session_end_entry(tmp_path, monkeypatch):
    """A prior repowire version wrote an inert SessionEnd group; reinstall
    cleans it up while preserving user-owned entries for the same event."""
    import json

    home = _retarget(tmp_path, monkeypatch)
    home.mkdir(parents=True)
    codex_mod.HOOKS_PATH.write_text(json.dumps({
        "hooks": {
            "SessionEnd": [
                {"hooks": [
                    {"type": "command", "command": "repowire hook session --backend=codex"},
                ]},
                {"hooks": [{"type": "command", "command": "my-own-tool"}]},
            ],
        }
    }))
    codex_mod.install_hooks()
    hooks = json.loads(codex_mod.HOOKS_PATH.read_text())["hooks"]
    assert hooks["SessionEnd"] == [
        {"hooks": [{"type": "command", "command": "my-own-tool"}]}
    ]


def test_install_hooks_reports_unchanged_on_reinstall(tmp_path, monkeypatch):
    _retarget(tmp_path, monkeypatch)
    assert codex_mod.install_hooks() is True
    assert codex_mod.install_hooks() is False


def test_trusted_hash_matches_codex_fingerprints():
    """Test vectors taken from a real codex install's [hooks.state] — the
    hashes codex itself computed for these exact entries. Breaking this means
    codex changed its NormalizedHookIdentity serialization."""
    assert codex_mod.trusted_hash_for(
        "SessionStart", "repowire hook session --backend=codex", "startup|resume|clear"
    ) == "sha256:de1c943b80a4f3e5d8380e6327f403d75f6dc26558c9f234e5626c56df072586"
    assert codex_mod.trusted_hash_for(
        "Stop", "repowire hook stop --backend=codex", None
    ) == "sha256:8c71eb47c0ea4dcc93a71a9bbf80cc1a223ff9c77e3aa159b16027240ffa255d"
    assert codex_mod.trusted_hash_for(
        "UserPromptSubmit", "repowire hook prompt --backend=codex", None
    ) == "sha256:4b89aa826ab51da3d57285efe28baf7ba29ff64b634787d0f9a44a3e6777bbfa"


def test_install_hooks_writes_trust_state(tmp_path, monkeypatch):
    """Install must pre-trust the entries in config.toml — codex silently
    skips untrusted hooks, which would disable the whole codex transport."""
    _retarget(tmp_path, monkeypatch)
    codex_mod.install_hooks()
    content = codex_mod.CONFIG_PATH.read_text()
    key = f'{codex_mod.HOOKS_PATH}:stop:0:0'
    assert f'[hooks.state."{key}"]' in content
    assert (
        'trusted_hash = "sha256:8c71eb47c0ea4dcc93a71a9bbf80cc1a223ff9c77e3aa159b16027240ffa255d"'
        in content
    )


def test_trust_state_upsert_replaces_existing_hash(tmp_path, monkeypatch):
    _retarget(tmp_path, monkeypatch)
    key = f"{codex_mod.HOOKS_PATH}:stop:0:0"
    codex_mod.CODEX_HOME.mkdir(parents=True)
    codex_mod.CONFIG_PATH.write_text(
        f'# my config\nmodel = "gpt-5"\n\n[hooks.state."{key}"]\n'
        'trusted_hash = "sha256:stale"\n\n[other]\nkeep = true\n'
    )
    codex_mod.install_hooks()
    content = codex_mod.CONFIG_PATH.read_text()
    assert content.count(f'[hooks.state."{key}"]') == 1
    assert 'trusted_hash = "sha256:stale"' not in content
    assert '# my config' in content
    assert 'keep = true' in content


def test_trust_state_indexes_account_for_user_groups(tmp_path, monkeypatch):
    """The state key is positional — a user-owned group before ours shifts
    our group index, and the written key must follow it."""
    import json

    home = _retarget(tmp_path, monkeypatch)
    home.mkdir(parents=True)
    codex_mod.HOOKS_PATH.write_text(json.dumps({
        "hooks": {
            "Stop": [{"hooks": [{"type": "command", "command": "my-own-tool"}]}],
        }
    }))
    codex_mod.install_hooks()
    content = codex_mod.CONFIG_PATH.read_text()
    assert f'[hooks.state."{codex_mod.HOOKS_PATH}:stop:1:0"]' in content
    assert f'[hooks.state."{codex_mod.HOOKS_PATH}:stop:0:0"]' not in content


def test_uninstall_hooks_round_trips(tmp_path, monkeypatch):
    import json

    _retarget(tmp_path, monkeypatch)
    codex_mod.install_hooks()
    assert codex_mod.uninstall_hooks() is True
    data = json.loads(codex_mod.HOOKS_PATH.read_text())
    assert data.get("hooks", {}) == {}
