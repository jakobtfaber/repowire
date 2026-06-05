"""Tests for tmux lifecycle hook registration."""

from __future__ import annotations

from unittest.mock import MagicMock, patch

from repowire.hooks._tmux import is_tmux_available
from repowire.hooks.tmux_lifecycle import (
    _HOOKS,
    inspect_lifecycle_hooks,
    install_hooks,
    uninstall_hooks,
)

_HOOK_NAMES = {name for name, _, _ in _HOOKS}

_FINGERPRINT_BY_HOOK = {
    "pane-exited": "/hooks/lifecycle/pane-died",
    "session-closed": "/hooks/lifecycle/session-closed",
    "after-rename-session": "tmux_rename_hook.sh /hooks/lifecycle/session-renamed",
    "after-rename-window": "tmux_rename_hook.sh /hooks/lifecycle/window-renamed",
    "client-detached": "/hooks/lifecycle/client-detached",
}


def _verified_tmux_run(args, **_kwargs):
    if args[1] == "set-hook":
        return MagicMock(returncode=0, stderr="")
    if args[1] == "show-hooks":
        slot = args[3]
        hook_name = slot.split("[", 1)[0]
        command = f'{slot} run-shell -b "repowire {_FINGERPRINT_BY_HOOK[hook_name]}"'
        return MagicMock(returncode=0, stdout=command, stderr="")
    return MagicMock(returncode=0, stdout="", stderr="")


def _set_hook_calls(mock_run):
    return [
        call for call in mock_run.call_args_list
        if call[0][0][1] == "set-hook"
    ]


class TestIsTmuxAvailable:
    def test_no_tmux_binary(self):
        with patch("repowire.hooks._tmux.shutil.which", return_value=None):
            assert is_tmux_available() is False

    def test_tmux_binary_but_no_server(self):
        with (
            patch("repowire.hooks._tmux.shutil.which", return_value="/usr/bin/tmux"),
            patch("repowire.hooks._tmux.subprocess.run") as mock_run,
        ):
            mock_run.return_value = MagicMock(returncode=1)
            assert is_tmux_available() is False

    def test_tmux_available_and_running(self):
        with (
            patch("repowire.hooks._tmux.shutil.which", return_value="/usr/bin/tmux"),
            patch("repowire.hooks._tmux.subprocess.run") as mock_run,
        ):
            mock_run.return_value = MagicMock(returncode=0)
            assert is_tmux_available() is True


class TestInstallHooks:
    def test_installs_all_hooks(self):
        with patch("subprocess.run") as mock_run:
            mock_run.side_effect = _verified_tmux_run
            result = install_hooks("127.0.0.1", 8377)

        assert set(result) == _HOOK_NAMES
        set_calls = _set_hook_calls(mock_run)
        assert len(set_calls) == len(_HOOK_NAMES)

        for call in set_calls:
            args = call[0][0]
            assert args[0] == "tmux"
            assert args[1] == "set-hook"
            assert args[2] in ("-g", "-gw")
            assert "[42]" in args[3]
            assert args[4].startswith("run-shell -b -- ")

    def test_window_hooks_use_gw_flag(self):
        with patch("subprocess.run") as mock_run:
            mock_run.side_effect = _verified_tmux_run
            install_hooks()

        hook_flags = {}
        for call in _set_hook_calls(mock_run):
            args = call[0][0]
            name = args[3].split("[")[0]
            hook_flags[name] = args[2]

        assert hook_flags["pane-exited"] == "-gw"
        assert hook_flags["after-rename-window"] == "-gw"
        assert hook_flags["session-closed"] == "-g"
        assert hook_flags["after-rename-session"] == "-g"
        assert hook_flags["client-detached"] == "-g"

    def test_partial_failure(self):
        call_count = 0

        def side_effect(*args, **kwargs):
            nonlocal call_count
            cmd = args[0]
            if cmd[1] == "show-hooks":
                return _verified_tmux_run(cmd, **kwargs)
            call_count += 1
            if call_count == 1:
                return MagicMock(returncode=1, stderr="error")
            return MagicMock(returncode=0)

        with patch("subprocess.run", side_effect=side_effect):
            result = install_hooks()

        assert len(result) == len(_HOOK_NAMES) - 1

    def test_curl_commands_contain_correct_url(self):
        with patch("subprocess.run") as mock_run:
            mock_run.side_effect = _verified_tmux_run
            install_hooks("10.0.0.1", 9999)

        for call in _set_hook_calls(mock_run):
            tmux_cmd = call[0][0][4]
            assert "10.0.0.1:9999" in tmux_cmd

    def test_curl_payloads_are_quoted_for_tmux_parser(self):
        with patch("subprocess.run") as mock_run:
            mock_run.side_effect = _verified_tmux_run
            install_hooks()

        tmux_commands = {
            call[0][0][3].split("[")[0]: call[0][0][4]
            for call in _set_hook_calls(mock_run)
        }

        assert tmux_commands["pane-exited"].startswith("run-shell -b -- '")
        assert '"Content-Type: application/json"' in tmux_commands["pane-exited"]
        assert '\\"#{hook_pane}\\"' in tmux_commands["pane-exited"]
        assert "#{pane_id}" not in tmux_commands["pane-exited"]

    def test_set_hook_success_with_empty_readback_is_not_installed(self):
        def side_effect(args, **_kwargs):
            if args[1] == "set-hook":
                return MagicMock(returncode=0, stderr="")
            return MagicMock(returncode=0, stdout=f"{args[3]} ", stderr="")

        with patch("subprocess.run", side_effect=side_effect):
            result = install_hooks()

        assert result == []

    def test_inspect_lifecycle_hooks_accepts_valid_readback(self):
        with patch("subprocess.run", side_effect=_verified_tmux_run):
            inspections = inspect_lifecycle_hooks()

        assert {inspection.status for inspection in inspections} == {"ok"}


class TestUninstallHooks:
    def test_uninstalls_all_hooks(self):
        with patch("subprocess.run") as mock_run:
            mock_run.return_value = MagicMock(returncode=0)
            result = uninstall_hooks()

        assert set(result) == _HOOK_NAMES

        for call in mock_run.call_args_list:
            args = call[0][0]
            assert args[0] == "tmux"
            assert args[1] == "set-hook"
            assert args[2] in ("-gu", "-gwu")
            assert "[42]" in args[3]

    def test_handles_missing_hooks(self):
        with patch("subprocess.run") as mock_run:
            mock_run.return_value = MagicMock(returncode=1)
            result = uninstall_hooks()

        assert result == []
