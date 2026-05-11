"""Tests for role plumbing through spawn_hints."""

from __future__ import annotations

from pathlib import Path

import pytest

from repowire.spawn_hints import consume_hint, consume_hint_full, write_hint


@pytest.fixture
def tmp_cache(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> Path:
    monkeypatch.setattr("repowire.spawn_hints.CACHE_DIR", tmp_path)
    return tmp_path


def test_write_hint_with_role_roundtrips(tmp_cache: Path) -> None:
    write_hint("/tmp/proj", "claude-code", "default", role="orchestrator")
    data = consume_hint_full("/tmp/proj", "claude-code")
    assert data is not None
    assert data["circle"] == "default"
    assert data["role"] == "orchestrator"


def test_write_hint_without_role_omits_field(tmp_cache: Path) -> None:
    write_hint("/tmp/proj", "claude-code", "default")
    data = consume_hint_full("/tmp/proj", "claude-code")
    assert data is not None
    assert data["circle"] == "default"
    assert "role" not in data


def test_legacy_consume_hint_returns_just_circle(tmp_cache: Path) -> None:
    write_hint("/tmp/proj", "claude-code", "default", role="orchestrator")
    assert consume_hint("/tmp/proj", "claude-code") == "default"


def test_consume_full_returns_none_when_missing(tmp_cache: Path) -> None:
    assert consume_hint_full("/tmp/never", "claude-code") is None
