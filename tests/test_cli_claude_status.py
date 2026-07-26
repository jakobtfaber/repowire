from unittest.mock import patch

from click.testing import CliRunner

from repowire.cli import main


def test_claude_status_uses_configured_transport_hook_check() -> None:
    with patch(
        "repowire.installers.claude_code.check_configured_hooks_installed",
        return_value=True,
    ) as check:
        result = CliRunner().invoke(main, ["claude", "status"])

    assert result.exit_code == 0
    assert "Hooks are installed." in result.output
    check.assert_called_once_with()


def test_claude_channel_ready_exits_zero_silently_when_ready() -> None:
    with patch(
        "repowire.installers.claude_code.check_channel_installed",
        return_value=True,
    ) as check:
        result = CliRunner().invoke(main, ["claude", "channel-ready"])

    assert result.exit_code == 0
    assert result.output == ""
    check.assert_called_once_with()


def test_claude_channel_ready_exits_one_silently_when_not_ready() -> None:
    with patch(
        "repowire.installers.claude_code.check_channel_installed",
        return_value=False,
    ) as check:
        result = CliRunner().invoke(main, ["claude", "channel-ready"])

    assert result.exit_code == 1
    assert result.output == ""
    check.assert_called_once_with()


def test_claude_channel_ready_is_hidden_from_help() -> None:
    result = CliRunner().invoke(main, ["claude", "--help"])

    assert result.exit_code == 0
    assert "channel-ready" not in result.output
