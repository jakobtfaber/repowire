"""HTTP tests for session-targeted control routes."""

from __future__ import annotations

from types import SimpleNamespace

import pytest
from httpx import ASGITransport, AsyncClient

from repowire.config.models import Config
from repowire.daemon import app as app_mod
from repowire.daemon.peer_delivery import NotifyDeliveryResult

pytestmark = pytest.mark.anyio


async def _register_bound_peer(client: AsyncClient) -> str:
    response = await client.post(
        "/peers",
        json={
            "name": "worker",
            "path": "/repo",
            "circle": "default",
            "backend": "claude-code",
            "metadata": {
                "hook_session_id": "runtime-active-1",
                "runtime_source_uri": "claude-jsonl:repo/runtime-active-1.jsonl",
            },
        },
    )
    assert response.status_code == 200, response.text
    return response.json()["peer_id"]


class _FakePeerDelivery:
    def __init__(self) -> None:
        self.calls: list[dict[str, object]] = []

    async def notify_result(self, **kwargs):
        self.calls.append(kwargs)
        return NotifyDeliveryResult(
            status="sent",
            delivery_state="delivered",
            reason="transport_delivered",
            from_peer_id=None,
            from_peer_name=kwargs["from_peer"],
            to_peer_id=kwargs["to_peer"],
            to_peer_name="worker-claude-code",
        )


class _FakeSpawnService:
    def __init__(self) -> None:
        self.calls: list[dict[str, object]] = []

    def spawn(self, **kwargs):
        self.calls.append(kwargs)
        return SimpleNamespace(
            display_name="repo-codex",
            tmux_session="repowire-repo-codex",
            pane_id="%99",
            message=None,
        )


async def test_session_resume_returns_active_executor(tmp_path):
    cfg = Config(experiments={"sqlite_state": True})
    app = app_mod.create_test_app(config=cfg, persistence_path=tmp_path / "sessions.json")

    async with app.router.lifespan_context(app):
        transport = ASGITransport(app=app)
        async with AsyncClient(transport=transport, base_url="http://test") as client:
            peer_id = await _register_bound_peer(client)
            binding = app.state.session_binding_store.get_by_runtime_session(
                "runtime-active-1",
                backend="claude-code",
                project_path="/repo",
            )
            assert binding is not None

            response = await client.post(
                f"/sessions/{binding.repowire_session_id}/controls/resume",
                json={},
            )

    assert response.status_code == 200, response.text
    body = response.json()
    assert body["status"] == "active_executor"
    assert body["capability"] == "active_executor"
    assert body["executor_peer_id"] == peer_id
    assert body["runtime_session_id"] == "runtime-active-1"


async def test_session_resume_reports_supported_backend_capability(tmp_path):
    cfg = Config(experiments={"sqlite_state": True})
    app = app_mod.create_test_app(config=cfg, persistence_path=tmp_path / "sessions.json")

    async with app.router.lifespan_context(app):
        binding = app.state.session_binding_store.upsert_observation(
            peer_id=None,
            backend="codex",
            project_path="/repo",
            runtime_session_id="codex-runtime-1",
            runtime_source_uri="codex-rollout:repo/codex-runtime-1.jsonl",
            resume_capability={
                "supported": True,
                "strategy": "codex_resume",
                "runtime_session_id_arg": "codex-runtime-1",
            },
            status="resumable",
        )
        transport = ASGITransport(app=app)
        async with AsyncClient(transport=transport, base_url="http://test") as client:
            response = await client.post(
                f"/sessions/{binding.repowire_session_id}/controls/resume",
                json={},
            )

    assert response.status_code == 200, response.text
    body = response.json()
    assert body["status"] == "resume_available"
    assert body["capability"] == "supported"
    assert body["backend"] == "codex"
    assert body["resume_capability"]["strategy"] == "codex_resume"


async def test_session_resume_executes_backend_resume_when_requested(tmp_path):
    cfg = Config(experiments={"sqlite_state": True})
    app = app_mod.create_test_app(config=cfg, persistence_path=tmp_path / "sessions.json")

    async with app.router.lifespan_context(app):
        fake_spawn = _FakeSpawnService()
        app.state.spawn_service = fake_spawn
        binding = app.state.session_binding_store.upsert_observation(
            peer_id=None,
            backend="codex",
            project_path="/repo",
            runtime_session_id="codex-runtime-1",
            runtime_source_uri="codex-rollout:repo/codex-runtime-1.jsonl",
            resume_capability={
                "supported": True,
                "strategy": "codex_resume",
                "runtime_session_id_arg": "codex-runtime-1",
            },
            status="resumable",
        )
        transport = ASGITransport(app=app)
        async with AsyncClient(transport=transport, base_url="http://test") as client:
            response = await client.post(
                f"/sessions/{binding.repowire_session_id}/controls/resume",
                json={"dry_run": False, "profile": "fast", "message": "resume please"},
            )

    assert response.status_code == 200, response.text
    body = response.json()
    assert body["status"] == "resume_available"
    assert body["capability"] == "supported"
    assert body["action"] == "spawned"
    assert body["spawned_display_name"] == "repo-codex"
    assert body["tmux_session"] == "repowire-repo-codex"
    assert body["pane_id"] == "%99"
    assert body["message"] == "Backend resume spawned for this runtime session."
    assert len(fake_spawn.calls) == 1
    call = fake_spawn.calls[0]
    assert call["path"] == "/repo"
    assert call["backend"].value == "codex"
    assert call["profile"] == "fast"
    assert call["message"] == "resume please"
    assert call["resume_plan"].runtime_session_id == "codex-runtime-1"
    assert call["resume_plan"].repowire_session_id == binding.repowire_session_id


async def test_session_resume_reports_unsupported_fallback(tmp_path):
    cfg = Config(experiments={"sqlite_state": True})
    app = app_mod.create_test_app(config=cfg, persistence_path=tmp_path / "sessions.json")

    async with app.router.lifespan_context(app):
        binding = app.state.session_binding_store.upsert_observation(
            peer_id=None,
            backend="mcp-http",
            project_path="/repo",
            runtime_session_id="mcp-http-runtime-1",
            status="detached",
        )
        transport = ASGITransport(app=app)
        async with AsyncClient(transport=transport, base_url="http://test") as client:
            response = await client.post(
                f"/sessions/{binding.repowire_session_id}/controls/resume",
                json={},
            )

    assert response.status_code == 200, response.text
    body = response.json()
    assert body["status"] == "unsupported"
    assert body["capability"] == "unsupported"
    assert "service identity" in body["message"]


async def test_session_resume_reports_legacy_binding_without_runtime_id(tmp_path):
    cfg = Config(experiments={"sqlite_state": True})
    app = app_mod.create_test_app(config=cfg, persistence_path=tmp_path / "sessions.json")

    async with app.router.lifespan_context(app):
        binding = app.state.session_binding_store.upsert_observation(
            peer_id=None,
            backend="codex",
            project_path="/repo",
            runtime_session_id=None,
            status="detached",
        )
        transport = ASGITransport(app=app)
        async with AsyncClient(transport=transport, base_url="http://test") as client:
            response = await client.post(
                f"/sessions/{binding.repowire_session_id}/controls/resume",
                json={},
            )

    assert response.status_code == 200, response.text
    body = response.json()
    assert body["status"] == "unsupported"
    assert body["capability"] == "unavailable"
    assert "without a runtime session id" in body["message"]


async def test_session_notify_targets_active_executor(tmp_path):
    cfg = Config(experiments={"sqlite_state": True})
    app = app_mod.create_test_app(config=cfg, persistence_path=tmp_path / "sessions.json")

    async with app.router.lifespan_context(app):
        fake_delivery = _FakePeerDelivery()
        app.state.peer_delivery = fake_delivery
        transport = ASGITransport(app=app)
        async with AsyncClient(transport=transport, base_url="http://test") as client:
            peer_id = await _register_bound_peer(client)
            binding = app.state.session_binding_store.get_by_runtime_session(
                "runtime-active-1",
                backend="claude-code",
                project_path="/repo",
            )
            assert binding is not None
            response = await client.post(
                f"/sessions/{binding.repowire_session_id}/controls/notify",
                json={"from_peer": "dashboard", "text": "continue this work"},
            )

    assert response.status_code == 200, response.text
    body = response.json()
    assert body["capability"] == "active_executor"
    assert body["executor_peer_id"] == peer_id
    assert body["delivery_state"] == "delivered"
    assert fake_delivery.calls == [
        {
            "from_peer": "dashboard",
            "to_peer": peer_id,
            "text": "continue this work",
            "bypass_circle": True,
            "attachments": [],
        }
    ]


async def test_session_notify_reports_resume_available_when_no_executor(tmp_path):
    cfg = Config(experiments={"sqlite_state": True})
    app = app_mod.create_test_app(config=cfg, persistence_path=tmp_path / "sessions.json")

    async with app.router.lifespan_context(app):
        binding = app.state.session_binding_store.upsert_observation(
            peer_id=None,
            backend="codex",
            project_path="/repo",
            runtime_session_id="codex-runtime-1",
            runtime_source_uri="codex-rollout:repo/codex-runtime-1.jsonl",
            resume_capability={
                "supported": True,
                "strategy": "codex_resume",
                "runtime_session_id_arg": "codex-runtime-1",
            },
            status="resumable",
        )
        transport = ASGITransport(app=app)
        async with AsyncClient(transport=transport, base_url="http://test") as client:
            response = await client.post(
                f"/sessions/{binding.repowire_session_id}/controls/notify",
                json={"from_peer": "dashboard", "text": "continue this work"},
            )

    assert response.status_code == 409
    body = response.json()["detail"]
    assert body["error"] == "session_executor_unavailable"
    assert body["status"] == "resume_available"
    assert body["capability"] == "supported"
    assert "Backend resume is available" in body["message"]


async def test_session_controls_report_missing_session_with_legacy_flag_false(tmp_path):
    cfg = Config(experiments={"sqlite_state": False})
    app = app_mod.create_test_app(config=cfg, persistence_path=tmp_path / "sessions.json")

    async with app.router.lifespan_context(app):
        transport = ASGITransport(app=app)
        async with AsyncClient(transport=transport, base_url="http://test") as client:
            response = await client.post("/sessions/rw-session-missing/controls/resume", json={})

    assert response.status_code == 404
    assert response.json()["detail"]["error"] == "session_not_found"
