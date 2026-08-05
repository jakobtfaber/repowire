import pytest
from click.testing import CliRunner

from repowire.cli import main


def test_codex_hook_is_dormant_without_opt_in(monkeypatch):
    called = False

    def handler(*, backend):
        nonlocal called
        called = True
        return 0

    monkeypatch.setattr("repowire.hooks.prompt_handler.main", handler)
    result = CliRunner().invoke(main, ["hook", "prompt", "--backend=codex"])

    assert result.exit_code == 0
    assert called is False


def test_codex_hook_runs_with_opt_in(monkeypatch):
    called = False

    def handler(*, backend):
        nonlocal called
        called = backend == "codex"
        return 0

    monkeypatch.setattr("repowire.hooks.prompt_handler.main", handler)
    result = CliRunner().invoke(
        main,
        ["hook", "prompt", "--backend=codex"],
        env={"REPOWIRE_CODEX_OPT_IN": "1"},
    )

    assert result.exit_code == 0
    assert called is True


def test_claude_hook_is_dormant_without_opt_in(monkeypatch):
    called = False

    def handler(*, backend):
        nonlocal called
        called = True
        return 0

    monkeypatch.setattr("repowire.hooks.prompt_handler.main", handler)
    result = CliRunner().invoke(main, ["hook", "prompt"])

    assert result.exit_code == 0
    assert called is False


def test_claude_hook_runs_with_opt_in(monkeypatch):
    called = False

    def handler(*, backend):
        nonlocal called
        called = backend == "claude-code"
        return 0

    monkeypatch.setattr("repowire.hooks.prompt_handler.main", handler)
    result = CliRunner().invoke(
        main,
        ["hook", "prompt"],
        env={"REPOWIRE_CLAUDE_OPT_IN": "1"},
    )

    assert result.exit_code == 0
    assert called is True


@pytest.mark.parametrize(
    ("target", "arguments"),
    [
        ("repowire.hooks.stop_handler.main", ["hook", "stop"]),
        ("repowire.hooks.session_handler.main", ["hook", "session"]),
        ("repowire.hooks.prompt_handler.main", ["hook", "prompt"]),
        ("repowire.hooks.notification_handler.main", ["hook", "notification"]),
        ("repowire.hooks.pretooluse_handler.main", ["hook", "pretooluse"]),
    ],
)
def test_every_claude_hook_is_dormant_without_opt_in(
    monkeypatch, target, arguments,
):
    def unexpected(*args, **kwargs):
        raise AssertionError("dormant Claude hook invoked its handler")

    monkeypatch.setattr(target, unexpected)

    result = CliRunner().invoke(main, arguments)

    assert result.exit_code == 0
