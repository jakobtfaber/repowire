from __future__ import annotations

import json
from pathlib import Path

from click.testing import CliRunner

from repowire.cli import main


def _patch_spawn_allowed(monkeypatch, allowed: bool = True) -> None:
    monkeypatch.setattr("repowire.cli._path_under_spawn_allowed_paths", lambda path: allowed)


def test_agents_create_uses_repo_local_default(tmp_path: Path, monkeypatch) -> None:
    monkeypatch.chdir(tmp_path)
    _patch_spawn_allowed(monkeypatch)

    result = CliRunner().invoke(main, ["agents", "create", "daily-brief", "--backend", "codex"])

    target = tmp_path / ".repowire" / "agents" / "daily-brief"
    assert result.exit_code == 0, result.output
    assert (target / "AGENTS.md").exists()
    assert (target / "CLAUDE.md").is_symlink()
    assert (target / "CLAUDE.md").readlink() == Path("AGENTS.md")
    assert not (target / "CODEX.md").exists()
    assert not (target / "GEMINI.md").exists()
    agents_md = (target / "AGENTS.md").read_text(encoding="utf-8")
    assert "## I/O Policy" in agents_md
    assert "result_surface" in agents_md
    assert "does not automatically deliver results" in agents_md
    assert "Always update the durable job result" in " ".join(agents_md.split())
    assert "repowire jobs create" in result.output
    assert "--backend codex" in result.output


def test_agents_create_rejects_invalid_name(tmp_path: Path, monkeypatch) -> None:
    monkeypatch.chdir(tmp_path)

    result = CliRunner().invoke(main, ["agents", "create", "../bad"])

    assert result.exit_code != 0
    assert "Agent name must match" in result.output


def test_agents_create_accepts_explicit_path(tmp_path: Path, monkeypatch) -> None:
    target = tmp_path / "workers" / "brief"
    _patch_spawn_allowed(monkeypatch)

    result = CliRunner().invoke(
        main,
        ["agents", "create", "brief", "--path", str(target), "--json"],
    )

    assert result.exit_code == 0, result.output
    assert (target / "AGENTS.md").exists()
    assert json.loads(result.output)["path"] == str(target.resolve())


def test_agents_create_idempotent_existing_scaffold(tmp_path: Path, monkeypatch) -> None:
    monkeypatch.chdir(tmp_path)
    _patch_spawn_allowed(monkeypatch)
    runner = CliRunner()
    first = runner.invoke(main, ["agents", "create", "brief"])
    second = runner.invoke(main, ["agents", "create", "brief"])

    assert first.exit_code == 0, first.output
    assert second.exit_code == 0, second.output
    assert "already exists" in second.output


def test_agents_create_refuses_existing_file_path(tmp_path: Path, monkeypatch) -> None:
    target = tmp_path / "brief"
    target.write_text("not a dir", encoding="utf-8")
    _patch_spawn_allowed(monkeypatch)

    result = CliRunner().invoke(main, ["agents", "create", "brief", "--path", str(target)])

    assert result.exit_code != 0
    assert "not a directory" in result.output


def test_agents_create_force_backs_up_existing_scaffold(tmp_path: Path, monkeypatch) -> None:
    monkeypatch.chdir(tmp_path)
    _patch_spawn_allowed(monkeypatch)
    runner = CliRunner()
    runner.invoke(main, ["agents", "create", "brief"])
    target = tmp_path / ".repowire" / "agents" / "brief"
    (target / "custom.txt").write_text("keep me", encoding="utf-8")

    result = runner.invoke(main, ["agents", "create", "brief", "--force"])

    assert result.exit_code == 0, result.output
    backups = list(target.parent.glob("brief.bak.*"))
    assert len(backups) == 1
    assert (backups[0] / "custom.txt").read_text(encoding="utf-8") == "keep me"
    assert (target / "AGENTS.md").exists()


def test_agents_create_json_payload(tmp_path: Path, monkeypatch) -> None:
    monkeypatch.chdir(tmp_path)
    _patch_spawn_allowed(monkeypatch, allowed=False)

    result = CliRunner().invoke(main, ["agents", "create", "brief", "--json"])

    assert result.exit_code == 0, result.output
    payload = json.loads(result.output)
    assert payload["name"] == "brief"
    assert payload["created"] is True
    assert payload["spawn_allowed"] is False
    assert payload["path"].endswith(".repowire/agents/brief")


def test_agents_create_copies_claude_when_symlink_fails(tmp_path: Path, monkeypatch) -> None:
    monkeypatch.chdir(tmp_path)
    _patch_spawn_allowed(monkeypatch)

    def fail_symlink(self: Path, target: str) -> None:
        raise OSError("no links")

    monkeypatch.setattr(Path, "symlink_to", fail_symlink)

    result = CliRunner().invoke(main, ["agents", "create", "brief"])

    target = tmp_path / ".repowire" / "agents" / "brief"
    assert result.exit_code == 0, result.output
    assert not (target / "CLAUDE.md").is_symlink()
    assert (target / "CLAUDE.md").read_text(encoding="utf-8") == (
        target / "AGENTS.md"
    ).read_text(encoding="utf-8")
    assert "copied" in result.output


def test_agents_create_warns_when_gitignored(tmp_path: Path, monkeypatch) -> None:
    monkeypatch.chdir(tmp_path)
    _patch_spawn_allowed(monkeypatch)
    monkeypatch.setattr("repowire.cli._path_is_git_ignored", lambda path: True)

    result = CliRunner().invoke(main, ["agents", "create", "brief"])

    assert result.exit_code == 0, result.output
    assert "git-ignored" in result.output
