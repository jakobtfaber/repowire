"""Tests for /schedules HTTP routes."""

from __future__ import annotations

from datetime import datetime, timedelta, timezone
from unittest.mock import MagicMock

import pytest

from repowire.daemon.deps import cleanup_deps
from repowire.daemon.routes import schedules
from repowire.daemon.schedule_store import ScheduleStore

from .conftest import async_client_for, make_daemon_app


def _make_app(tmp_path):
    store = ScheduleStore(tmp_path / "schedules.json")
    # Mock scheduler — routes only need .notify_changed()
    scheduler = MagicMock()
    scheduler.notify_changed = MagicMock()
    harness = make_daemon_app(
        tmp_path,
        (schedules.router,),
        state_overrides={"schedule_store": store, "scheduler": scheduler},
    )
    return harness.app, store, scheduler


@pytest.fixture
async def env(tmp_path):
    app, store, scheduler = _make_app(tmp_path)
    async with async_client_for(app) as c:
        yield c, store, scheduler
    cleanup_deps()


def _future_iso(seconds: float = 60.0) -> str:
    return (datetime.now(timezone.utc) + timedelta(seconds=seconds)).isoformat()


class TestCreate:
    async def test_returns_schedule_id_and_wakes_scheduler(self, env):
        client, store, scheduler = env
        r = await client.post("/schedules", json={
            "from_peer": "alice",
            "to_peer": "bob",
            "text": "ping",
            "fire_at": _future_iso(),
        })
        assert r.status_code == 200
        body = r.json()
        assert body["schedule_id"].startswith("sched-")
        assert body["kind"] == "notify"
        assert store.get(body["schedule_id"]) is not None
        scheduler.notify_changed.assert_called_once()

    async def test_accepts_cron_schedule(self, env):
        client, store, scheduler = env
        r = await client.post("/schedules", json={
            "from_peer": "alice",
            "to_peer": "alice",
            "text": "stretch",
            "cron": "*/15 * * * *",
        })
        assert r.status_code == 200
        body = r.json()
        assert body["cron"] == "*/15 * * * *"
        assert body["fire_at"]
        assert store.get(body["schedule_id"]) is not None
        scheduler.notify_changed.assert_called_once()

    async def test_rejects_fire_at_and_cron_together(self, env):
        client, _, _ = env
        r = await client.post("/schedules", json={
            "from_peer": "alice", "to_peer": "bob",
            "text": "x", "fire_at": _future_iso(), "cron": "* * * * *",
        })
        assert r.status_code == 400

    async def test_rejects_invalid_fire_at(self, env):
        client, _, _ = env
        r = await client.post("/schedules", json={
            "from_peer": "alice", "to_peer": "bob",
            "text": "x", "fire_at": "not-a-date",
        })
        assert r.status_code == 400

    async def test_rejects_unknown_kind(self, env):
        client, _, _ = env
        r = await client.post("/schedules", json={
            "from_peer": "alice", "to_peer": "bob",
            "text": "x", "fire_at": _future_iso(), "kind": "recurring",
        })
        assert r.status_code == 400


class TestList:
    async def test_empty(self, env):
        client, _, _ = env
        r = await client.get("/schedules")
        assert r.status_code == 200
        assert r.json()["schedules"] == []

    async def test_filter_by_from_peer(self, env):
        client, _, _ = env
        await client.post("/schedules", json={
            "from_peer": "alice", "to_peer": "bob",
            "text": "a", "fire_at": _future_iso(60),
        })
        await client.post("/schedules", json={
            "from_peer": "eve", "to_peer": "bob",
            "text": "e", "fire_at": _future_iso(30),
        })
        r = await client.get("/schedules", params={"from_peer": "alice"})
        items = r.json()["schedules"]
        assert len(items) == 1
        assert items[0]["from_peer"] == "alice"


class TestDelete:
    async def test_removes_and_wakes(self, env):
        client, store, scheduler = env
        r = await client.post("/schedules", json={
            "from_peer": "alice", "to_peer": "bob",
            "text": "x", "fire_at": _future_iso(),
        })
        sid = r.json()["schedule_id"]
        scheduler.notify_changed.reset_mock()

        r = await client.delete(f"/schedules/{sid}")
        assert r.status_code == 200
        assert store.get(sid) is None
        scheduler.notify_changed.assert_called_once()

    async def test_unknown_returns_404(self, env):
        client, _, _ = env
        r = await client.delete("/schedules/sched-nope")
        assert r.status_code == 404
