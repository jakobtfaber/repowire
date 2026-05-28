from __future__ import annotations

from pathlib import Path
from types import SimpleNamespace
from unittest.mock import AsyncMock

import pytest
from fastapi import FastAPI
from httpx import ASGITransport, AsyncClient

from repowire.config.models import Config
from repowire.daemon.ask_tracker import AskTracker
from repowire.daemon.deps import cleanup_deps, init_deps
from repowire.daemon.message_router import MessageRouter
from repowire.daemon.peer_registry import PeerRegistry
from repowire.daemon.query_tracker import QueryTracker
from repowire.daemon.routes import work as work_routes
from repowire.daemon.state.calendar import SQLiteCalendarStore
from repowire.daemon.state.database import StateDatabase
from repowire.daemon.state.work import SQLiteWorkStore
from repowire.daemon.websocket_transport import WebSocketTransport


def _make_app(tmp_path: Path):
    cfg = Config()
    transport = WebSocketTransport()
    qt = QueryTracker()
    at = AskTracker(ttl_hours=24.0)
    router = MessageRouter(transport=transport, query_tracker=qt)
    registry = PeerRegistry(
        config=cfg,
        message_router=router,
        query_tracker=qt,
        transport=transport,
        persistence_path=tmp_path / "sessions.json",
        ask_tracker=at,
    )
    db = StateDatabase(tmp_path / "state.db")
    store = SQLiteWorkStore(db)
    calendar_store = SQLiteCalendarStore(db, store)

    state = SimpleNamespace(
        config=cfg,
        transport=transport,
        query_tracker=qt,
        ask_tracker=at,
        message_router=router,
        peer_registry=registry,
        work_store=store,
        calendar_store=calendar_store,
        relay_mode=False,
    )
    init_deps(cfg, registry, state)

    app = FastAPI()
    app.include_router(work_routes.router)
    return app, db


@pytest.fixture
async def env(tmp_path):
    app, db = _make_app(tmp_path)
    t = ASGITransport(app=app)
    async with AsyncClient(transport=t, base_url="http://test") as c:
        yield c
    cleanup_deps()
    db.close()


async def test_create_work_returns_queued_status(env) -> None:
    r = await env.post(
        "/work",
        json={
            "title": "Run checks",
            "kind": "verification",
            "created_by_peer_id": "repow-default-creator",
            "owner_peer_id": "repow-default-owner",
            "assigned_peer_id": "repow-default-worker",
            "circle": "default",
            "source_kind": "dashboard",
            "source_id": "compose-1",
            "request": {"title": "run checks"},
        },
    )

    assert r.status_code == 200
    body = r.json()
    assert body["work_id"].startswith("work-")
    assert body["job_id"] == body["work_id"]
    assert body["status"]["title"] == "Run checks"
    assert body["status"]["kind"] == "verification"
    assert body["status"]["state"] == "queued"
    assert body["status"]["owner_peer_id"] == "repow-default-owner"
    assert body["status"]["assigned_peer_id"] == "repow-default-worker"
    assert body["status"]["source_kind"] == "dashboard"


async def test_list_work_filters_by_circle_and_state(env) -> None:
    await env.post("/work", json={"circle": "default", "owner_peer_id": "owner-a"})
    await env.post("/work", json={"circle": "other", "owner_peer_id": "owner-b"})

    r = await env.get("/work", params={"circle": "default", "state": "queued"})

    assert r.status_code == 200
    items = r.json()["work"]
    assert len(items) == 1
    assert items[0]["owner_peer_id"] == "owner-a"


async def test_result_for_non_terminal_work_returns_not_ready_status(env) -> None:
    created = await env.post("/work", json={"created_by_peer_id": "creator"})
    work_id = created.json()["work_id"]

    r = await env.get(f"/work/{work_id}/result")

    assert r.status_code == 200
    result = r.json()["result"]
    assert result["result_state"] == "not_ready"
    assert result["status"]["work_id"] == work_id
    assert result["status"]["job_id"] == work_id


async def test_jobs_alias_can_update_progress_and_return_result(env) -> None:
    created = await env.post("/jobs", json={"title": "Investigate", "kind": "research"})
    job_id = created.json()["job_id"]

    updated = await env.patch(
        f"/jobs/{job_id}",
        json={
            "state": "completed",
            "phase": "done",
            "progress_note": "wrote summary",
            "result_summary": "root cause found",
            "result_data": {"root_cause": "missing config"},
        },
    )

    assert updated.status_code == 200
    status = updated.json()["status"]
    assert status["state"] == "completed"
    assert status["progress_events"][0]["note"] == "wrote summary"

    result = await env.get(f"/jobs/{job_id}/result")
    assert result.status_code == 200
    assert result.json()["result"]["summary"] == "root cause found"


async def test_jobs_create_with_cron_returns_recurring_calendar(env) -> None:
    created = await env.post(
        "/jobs",
        json={
            "title": "Daily brief",
            "kind": "brief",
            "cron": "@daily",
            "prompt": "write the brief",
            "path": "/tmp/brief",
            "backend": "codex",
        },
    )

    assert created.status_code == 200
    body = created.json()
    assert body["calendar_id"].startswith("cal-")
    assert body["recurring_id"] == body["calendar_id"]
    assert body["calendar"]["cron"] == "0 0 * * *"
    assert body["calendar"]["execution"]["target"] == {
        "path": "/tmp/brief",
        "backend": "codex",
    }
    assert body["calendar"]["execution"]["process_scope"] == "per_fire"
    assert body["calendar"]["execution"]["continuity"] == "resume"


async def test_jobs_create_rejects_due_at_and_cron_together(env) -> None:
    created = await env.post(
        "/jobs",
        json={
            "title": "Bad time",
            "due_at": "2026-05-26T08:00:00Z",
            "cron": "@daily",
        },
    )

    assert created.status_code == 400
    assert "not both" in created.json()["detail"]


async def test_jobs_list_and_show_include_recurring_calendar(env) -> None:
    created = await env.post("/jobs", json={"title": "Daily brief", "cron": "@daily"})
    calendar_id = created.json()["calendar_id"]

    listed = await env.get("/jobs")
    shown = await env.get(f"/jobs/{calendar_id}/status")

    assert listed.status_code == 200
    assert listed.json()["recurring"][0]["calendar_id"] == calendar_id
    assert shown.status_code == 200
    assert shown.json()["status"]["calendar_id"] == calendar_id


async def test_jobs_summary_view_omits_heavy_fields(env) -> None:
    created = await env.post(
        "/jobs",
        json={
            "title": "Heavy job",
            "prompt": "x" * 1000,
            "path": "/repo/repowire",
            "provenance": {"large": "y" * 1000},
        },
    )
    job_id = created.json()["job_id"]
    await env.patch(
        f"/jobs/{job_id}",
        json={
            "state": "completed",
            "result_summary": "done",
            "result_data": {"blob": "z" * 1000},
            "progress_note": "finished",
        },
    )

    full = await env.get("/jobs")
    listed = await env.get("/jobs?view=summary")
    shown = await env.get(f"/jobs/{job_id}/status")

    assert full.status_code == 200
    assert full.json()["work"][0]["request"]["execution"]["prompt"]["body"] == "x" * 1000
    assert "provenance" in full.json()["work"][0]
    assert listed.status_code == 200
    summary = listed.json()["work"][0]
    assert summary["job_id"] == job_id
    assert summary["title"] == "Heavy job"
    assert summary["state"] == "completed"
    assert summary["execution"]["target"]["path"] == "/repo/repowire"
    assert "request" not in summary
    assert "provenance" not in summary
    assert "progress_events" not in summary
    assert "runner" not in summary
    assert "prompt" not in summary["execution"]
    assert shown.json()["status"]["execution"]["prompt"]["body"] == "x" * 1000


async def test_jobs_summary_view_rejects_unknown_view(env) -> None:
    listed = await env.get("/jobs?view=compact")

    assert listed.status_code == 400
    assert "view must be" in listed.json()["detail"]


async def test_jobs_cancel_recurring_calendar_does_not_require_work(env) -> None:
    created = await env.post("/jobs", json={"title": "Daily brief", "cron": "@daily"})
    calendar_id = created.json()["calendar_id"]

    cancelled = await env.post(f"/jobs/{calendar_id}/cancel", json={"reason": "done"})

    assert cancelled.status_code == 200
    assert cancelled.json()["status"]["state"] == "cancelled"


async def test_terminal_job_cannot_move_back_to_non_terminal(env) -> None:
    created = await env.post("/jobs", json={"title": "Terminal guard"})
    job_id = created.json()["job_id"]
    completed = await env.patch(
        f"/jobs/{job_id}",
        json={"state": "completed", "result_summary": "done"},
    )
    assert completed.status_code == 200

    reverted = await env.patch(f"/jobs/{job_id}", json={"state": "running"})

    assert reverted.status_code == 400
    assert "terminal state cannot be changed" in reverted.json()["detail"]


async def test_terminal_same_state_update_preserves_result_fields(env) -> None:
    created = await env.post("/jobs", json={"title": "Preserve result"})
    job_id = created.json()["job_id"]
    failed = await env.patch(
        f"/jobs/{job_id}",
        json={
            "state": "failed",
            "result_summary": "failed",
            "result_data": {"step": "setup"},
            "error": {"message": "missing config"},
            "artifacts": [{"path": "logs/setup.txt"}],
        },
    )
    assert failed.status_code == 200

    enriched = await env.patch(
        f"/jobs/{job_id}",
        json={"state": "failed", "progress_note": "triaged"},
    )

    assert enriched.status_code == 200
    result = await env.get(f"/jobs/{job_id}/result")
    body = result.json()["result"]
    assert body["summary"] == "failed"
    assert body["data"] == {"step": "setup"}
    assert body["error"] == {"message": "missing config"}
    assert body["artifacts"] == [{"path": "logs/setup.txt"}]


async def test_cancel_queued_work_returns_cancelled_status(env) -> None:
    created = await env.post("/work", json={"owner_peer_id": "owner"})
    work_id = created.json()["work_id"]

    r = await env.post(
        f"/work/{work_id}/cancel",
        json={"requested_by_peer_id": "creator"},
    )

    assert r.status_code == 200
    status = r.json()["status"]
    assert status["state"] == "cancelled"
    assert status["state_reason"] == "cancel_requested"
    assert status["cancel_requested_by_peer_id"] == "creator"
    assert status["cancellation_reason"] == "cancel_requested"
    assert status["protocol_cancel"] is None


async def test_cancel_running_work_attempts_existing_protocol_cancel(env) -> None:
    created = await env.post(
        "/work",
        json={"assigned_peer_id": "repow-default-worker"},
    )
    work_id = created.json()["work_id"]
    await env.patch(f"/work/{work_id}", json={"state": "running"})
    state = work_routes.get_app_state()
    state.acp_manager = SimpleNamespace(
        cancel_existing=AsyncMock(
            return_value={
                "attempted": True,
                "status": "sent",
                "reason": "session_cancel_sent",
                "peer_id": "repow-default-worker",
            },
        ),
    )

    r = await env.post(
        f"/work/{work_id}/cancel",
        json={"requested_by_peer_id": "creator", "reason": "user_requested"},
    )

    assert r.status_code == 200
    state.acp_manager.cancel_existing.assert_awaited_once_with("repow-default-worker")
    status = r.json()["status"]
    assert status["state"] == "running"
    assert status["state_reason"] == "user_requested"
    assert status["cancel_requested"] is True
    assert status["protocol_cancel"] == {
        "attempted": True,
        "status": "sent",
        "reason": "session_cancel_sent",
        "peer_id": "repow-default-worker",
    }


async def test_cancel_running_work_without_protocol_link_stays_pending(env) -> None:
    created = await env.post("/work", json={"owner_peer_id": "owner"})
    work_id = created.json()["work_id"]
    await env.patch(f"/work/{work_id}", json={"state": "running"})

    r = await env.post(f"/work/{work_id}/cancel", json={})

    assert r.status_code == 200
    status = r.json()["status"]
    assert status["state"] == "running"
    assert status["cancel_requested"] is True
    assert status["protocol_cancel"] == {
        "attempted": False,
        "status": "unavailable",
        "reason": "no_assigned_peer",
    }


async def test_unknown_work_returns_404(env) -> None:
    r = await env.get("/work/work-missing/status")

    assert r.status_code == 404
