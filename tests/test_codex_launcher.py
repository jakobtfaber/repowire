import os
import sys

from repowire import codex_launcher


def test_launcher_opts_in_and_enables_mcp(monkeypatch):
    observed = {}

    def execute(file, arguments, environment):
        observed.update(file=file, arguments=arguments, environment=environment)

    monkeypatch.setenv("OPENAI_API_KEY", "secret")
    monkeypatch.setenv("CODEX_API_KEY", "secret")
    monkeypatch.setattr(sys, "argv", ["rwcodex", "--version"])
    monkeypatch.setattr(os, "execvpe", execute)

    codex_launcher.main()

    assert observed["file"] == "codex"
    assert observed["arguments"] == [
        "codex",
        "-c",
        "mcp_servers.repowire.enabled=true",
        "--version",
    ]
    assert observed["environment"]["REPOWIRE_CODEX_OPT_IN"] == "1"
    assert "OPENAI_API_KEY" not in observed["environment"]
    assert "CODEX_API_KEY" not in observed["environment"]
