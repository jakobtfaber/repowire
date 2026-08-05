import os
import sys

import pytest

from repowire import claude_launcher


def test_launcher_opts_in_and_adds_mcp_config(monkeypatch, tmp_path):
    observed = {}

    def execute(file, arguments, environment):
        observed.update(file=file, arguments=arguments, environment=environment)

    monkeypatch.setenv("HOME", str(tmp_path))
    config = tmp_path / ".repowire" / "claude-mcp.json"
    config.parent.mkdir()
    config.write_text("{}")
    monkeypatch.setattr(sys, "argv", ["rwclaude", "--version"])
    monkeypatch.setattr(os, "execvpe", execute)

    claude_launcher.main()

    assert observed["file"] == "claude"
    assert observed["arguments"] == [
        "claude",
        "--version",
        "--mcp-config",
        str(tmp_path / ".repowire" / "claude-mcp.json"),
    ]
    assert observed["environment"]["REPOWIRE_CLAUDE_OPT_IN"] == "1"


def test_launcher_inserts_mcp_config_before_delimiter(monkeypatch, tmp_path):
    observed = {}
    config = tmp_path / ".repowire" / "claude-mcp.json"
    config.parent.mkdir()
    config.write_text("{}")
    monkeypatch.setenv("HOME", str(tmp_path))
    monkeypatch.setattr(sys, "argv", ["rwclaude", "--", "prompt"])
    monkeypatch.setattr(
        os,
        "execvpe",
        lambda file, arguments, environment: observed.update(arguments=arguments),
    )

    claude_launcher.main()

    assert observed["arguments"] == [
        "claude", "--mcp-config", str(config), "--", "prompt"
    ]


def test_launcher_fails_cleanly_without_mcp_config(monkeypatch, tmp_path):
    monkeypatch.setenv("HOME", str(tmp_path))

    with pytest.raises(SystemExit, match="run `repowire setup`"):
        claude_launcher.main()
