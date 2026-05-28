"""Tests for telegram bot's default-route-to-orchestrator behavior."""

from __future__ import annotations

from types import SimpleNamespace
from typing import Any
from unittest.mock import AsyncMock

import pytest

from repowire.telegram.bot import OrchestratorStatus, TelegramPeer


@pytest.fixture
def bot() -> TelegramPeer:
    """Build a TelegramPeer with minimal config, no daemon connection."""
    return TelegramPeer(
        bot_token="test-token",
        chat_id="0",
        daemon_url="http://127.0.0.1:0",
        display_name="telegram",
        circle="default",
    )


@pytest.mark.asyncio
async def test_seeds_target_to_orchestrator_when_present(
    bot: TelegramPeer, monkeypatch: pytest.MonkeyPatch,
) -> None:
    fake_peers: list[dict[str, Any]] = [
        {"name": "some-agent", "role": "agent", "status": "online"},
        {"name": "orchestrator", "role": "orchestrator", "status": "online"},
    ]
    mock_fetch = AsyncMock(return_value=fake_peers)
    monkeypatch.setattr(bot, "_fetch_online_peers", mock_fetch)

    assert bot._reply_target is None
    await bot._seed_default_target_from_orchestrator()
    assert bot._reply_target == "orchestrator"


@pytest.mark.asyncio
async def test_does_not_override_existing_target(
    bot: TelegramPeer, monkeypatch: pytest.MonkeyPatch,
) -> None:
    bot._reply_target = "user-selected-peer"
    mock_fetch = AsyncMock(return_value=[
        {"name": "orchestrator", "role": "orchestrator", "status": "online"},
    ])
    monkeypatch.setattr(bot, "_fetch_online_peers", mock_fetch)

    await bot._seed_default_target_from_orchestrator()
    assert bot._reply_target == "user-selected-peer"
    mock_fetch.assert_not_called()  # short-circuits before fetch


@pytest.mark.asyncio
async def test_falls_through_when_no_orchestrator_online(
    bot: TelegramPeer, monkeypatch: pytest.MonkeyPatch,
) -> None:
    fake_peers: list[dict[str, Any]] = [
        {"name": "agent-1", "role": "agent", "status": "online"},
    ]
    monkeypatch.setattr(
        bot, "_fetch_online_peers", AsyncMock(return_value=fake_peers),
    )
    await bot._seed_default_target_from_orchestrator()
    assert bot._reply_target is None


@pytest.mark.asyncio
async def test_ignores_offline_orchestrator(
    bot: TelegramPeer, monkeypatch: pytest.MonkeyPatch,
) -> None:
    fake_peers: list[dict[str, Any]] = [
        {"name": "orchestrator", "role": "orchestrator", "status": "offline"},
    ]
    monkeypatch.setattr(
        bot, "_fetch_online_peers", AsyncMock(return_value=fake_peers),
    )
    await bot._seed_default_target_from_orchestrator()
    assert bot._reply_target is None


@pytest.mark.asyncio
async def test_accepts_busy_orchestrator(
    bot: TelegramPeer, monkeypatch: pytest.MonkeyPatch,
) -> None:
    fake_peers: list[dict[str, Any]] = [
        {"name": "orchestrator", "role": "orchestrator", "status": "busy"},
    ]
    monkeypatch.setattr(
        bot, "_fetch_online_peers", AsyncMock(return_value=fake_peers),
    )
    await bot._seed_default_target_from_orchestrator()
    assert bot._reply_target == "orchestrator"


@pytest.mark.asyncio
async def test_falls_back_to_orchestrator_named_workspace_peer(
    bot: TelegramPeer, monkeypatch: pytest.MonkeyPatch,
) -> None:
    fake_peers: list[dict[str, Any]] = [
        {
            "peer_id": "repow-default-live",
            "name": "orchestrator-claude-code",
            "display_name": "orchestrator-claude-code",
            "role": "agent",
            "status": "online",
            "circle": "default",
            "path": "/home/user/.repowire/orchestrator",
        },
    ]
    route = AsyncMock()
    route.return_value = SimpleNamespace(status_code=200, json=lambda: {"present": False})
    monkeypatch.setattr(bot._http, "get", route)
    monkeypatch.setattr(
        bot, "_fetch_online_peers", AsyncMock(return_value=fake_peers),
    )

    await bot._seed_default_target_from_orchestrator()

    assert bot._reply_target == "repow-default-live"


@pytest.mark.asyncio
async def test_stale_orchestrator_target_normalizes_before_send(
    bot: TelegramPeer, monkeypatch: pytest.MonkeyPatch,
) -> None:
    bot._display_name = "telegram-claude-code"
    monkeypatch.setattr(
        bot,
        "_fetch_orchestrator_status",
        AsyncMock(return_value=OrchestratorStatus(present=True, peer="repow-live-orch")),
    )
    post = AsyncMock()
    post.return_value.status_code = 200
    monkeypatch.setattr(bot._http, "post", post)

    await bot._ask("orchestrator-codex", "hello")

    assert bot._reply_target == "repow-live-orch"
    assert post.await_args.kwargs["json"]["to_peer"] == "repow-live-orch"


@pytest.mark.asyncio
async def test_no_active_text_reports_missing_orchestrator(
    bot: TelegramPeer, monkeypatch: pytest.MonkeyPatch,
) -> None:
    monkeypatch.setattr(
        bot,
        "_fetch_orchestrator_status",
        AsyncMock(return_value=OrchestratorStatus(present=False)),
    )
    send = AsyncMock()
    monkeypatch.setattr(bot, "_tg_send", send)

    await bot._on_text("hello mesh")

    send.assert_awaited_once()
    assert "No orchestrator online" in send.await_args.args[0]
    assert "repowire orchestrator start" in send.await_args.args[0]


@pytest.mark.asyncio
async def test_no_active_text_keeps_peer_picker_when_orchestrator_present(
    bot: TelegramPeer, monkeypatch: pytest.MonkeyPatch,
) -> None:
    monkeypatch.setattr(
        bot,
        "_fetch_orchestrator_status",
        AsyncMock(return_value=OrchestratorStatus(present=True, peer="orchestrator")),
    )
    send = AsyncMock()
    monkeypatch.setattr(bot, "_tg_send", send)

    await bot._on_text("hello mesh")

    send.assert_awaited_once()
    assert "No active conversation" in send.await_args.args[0]
