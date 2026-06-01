"""Tests for daemon-side orchestrator recall injection."""

from __future__ import annotations

from pathlib import Path
from types import SimpleNamespace
from unittest.mock import AsyncMock

import pytest

from repowire.config.models import Config
from repowire.daemon.peer_delivery import PeerDeliveryService
from repowire.daemon.transport_router import AskTransportResult, NotifyTransportResult
from repowire.protocol.peers import Peer, PeerRole, PeerStatus


def _peer(peer_id: str, name: str, *, role: PeerRole = PeerRole.AGENT) -> Peer:
    return Peer(
        peer_id=peer_id,
        display_name=name,
        path=f"/tmp/{name}",
        machine="localhost",
        circle="default",
        role=role,
        status=PeerStatus.ONLINE,
    )


def _write_orchestrator_memory(root: Path) -> None:
    root.mkdir(parents=True)
    (root / "comms.md").write_text(
        "# Comms\n\nTelegram replies should be concise and route via telegram-claude-code.\n",
        encoding="utf-8",
    )
    (root / "projects.md").write_text(
        "# Projects\n\nRepowire restart work is on main.\n",
        encoding="utf-8",
    )
    memory_dir = root / "memory"
    memory_dir.mkdir()
    (memory_dir / "telegram-routing.md").write_text(
        "# Telegram Routing\n\nUse @telegram-claude-code for Telegram coordination.\n",
        encoding="utf-8",
    )


def _service(
    *,
    config: Config,
    sender: Peer,
    target: Peer,
    transport_router: SimpleNamespace,
) -> PeerDeliveryService:
    registry = SimpleNamespace(
        check_access=AsyncMock(return_value=(sender, target)),
        add_event=lambda *args, **kwargs: None,
    )
    return PeerDeliveryService(
        registry=registry,  # type: ignore[arg-type]
        message_router=SimpleNamespace(),  # type: ignore[arg-type]
        transport_router=transport_router,  # type: ignore[arg-type]
        config=config,
    )


@pytest.mark.asyncio
async def test_orchestrator_notify_injects_recall_block(tmp_path, monkeypatch) -> None:
    workspace = tmp_path / "orchestrator"
    _write_orchestrator_memory(workspace)
    monkeypatch.setattr("repowire.daemon.orchestrator_recall.workspace_path", lambda: workspace)
    sender = _peer("sender-id", "telegram-claude-code", role=PeerRole.SERVICE)
    target = _peer("orch-id", "orchestrator-claude-code", role=PeerRole.ORCHESTRATOR)
    transport = SimpleNamespace(
        send_notify=AsyncMock(return_value=NotifyTransportResult(status="sent")),
    )

    service = _service(config=Config(), sender=sender, target=target, transport_router=transport)
    await service.notify_result(
        from_peer=sender.peer_id,
        to_peer=target.peer_id,
        text="Please answer this Telegram routing question.",
        bypass_circle=True,
    )

    envelope = transport.send_notify.await_args.args[0]
    assert envelope.text.startswith("[repowire recall]\n")
    assert "telegram-routing.md" in envelope.text
    assert "telegram-claude-code" in envelope.text
    assert "[/repowire recall]\n\nPlease answer this Telegram routing question." in envelope.text


@pytest.mark.asyncio
async def test_orchestrator_ask_injects_recall_block(tmp_path, monkeypatch) -> None:
    workspace = tmp_path / "orchestrator"
    _write_orchestrator_memory(workspace)
    monkeypatch.setattr("repowire.daemon.orchestrator_recall.workspace_path", lambda: workspace)
    sender = _peer("sender-id", "telegram-claude-code", role=PeerRole.SERVICE)
    target = _peer("orch-id", "orchestrator-claude-code", role=PeerRole.ORCHESTRATOR)
    transport = SimpleNamespace(
        acp_route=lambda _target: None,
        send_ask=AsyncMock(return_value=AskTransportResult(transport="ws")),
    )

    service = _service(config=Config(), sender=sender, target=target, transport_router=transport)
    await service.deliver_ask(
        from_peer=sender.peer_id,
        to_peer=target.peer_id,
        text="How should I route telegram updates?",
        correlation_id="ask-1",
        bypass_circle=True,
    )

    envelope = transport.send_ask.await_args.args[0]
    assert envelope.text.startswith("[repowire recall]\n")
    assert "Telegram replies should be concise" in envelope.text


@pytest.mark.asyncio
async def test_recall_does_not_apply_to_agent_role(tmp_path, monkeypatch) -> None:
    workspace = tmp_path / "orchestrator"
    _write_orchestrator_memory(workspace)
    monkeypatch.setattr("repowire.daemon.orchestrator_recall.workspace_path", lambda: workspace)
    sender = _peer("sender-id", "telegram-claude-code", role=PeerRole.SERVICE)
    target = _peer("agent-id", "worker-claude-code", role=PeerRole.AGENT)
    transport = SimpleNamespace(
        send_notify=AsyncMock(return_value=NotifyTransportResult(status="sent")),
    )

    service = _service(config=Config(), sender=sender, target=target, transport_router=transport)
    await service.notify_result(
        from_peer=sender.peer_id,
        to_peer=target.peer_id,
        text="Please answer this Telegram routing question.",
        bypass_circle=True,
    )

    envelope = transport.send_notify.await_args.args[0]
    assert envelope.text == "Please answer this Telegram routing question."


@pytest.mark.asyncio
async def test_recall_kill_switch_disables_injection(tmp_path, monkeypatch) -> None:
    workspace = tmp_path / "orchestrator"
    _write_orchestrator_memory(workspace)
    monkeypatch.setattr("repowire.daemon.orchestrator_recall.workspace_path", lambda: workspace)
    config = Config()
    config.daemon.orchestrator_recall.enabled = False
    sender = _peer("sender-id", "telegram-claude-code", role=PeerRole.SERVICE)
    target = _peer("orch-id", "orchestrator-claude-code", role=PeerRole.ORCHESTRATOR)
    transport = SimpleNamespace(
        send_notify=AsyncMock(return_value=NotifyTransportResult(status="sent")),
    )

    service = _service(config=config, sender=sender, target=target, transport_router=transport)
    await service.notify_result(
        from_peer=sender.peer_id,
        to_peer=target.peer_id,
        text="Please answer this Telegram routing question.",
        bypass_circle=True,
    )

    envelope = transport.send_notify.await_args.args[0]
    assert envelope.text == "Please answer this Telegram routing question."


@pytest.mark.asyncio
async def test_recall_no_match_passthrough(tmp_path, monkeypatch) -> None:
    workspace = tmp_path / "orchestrator"
    _write_orchestrator_memory(workspace)
    monkeypatch.setattr("repowire.daemon.orchestrator_recall.workspace_path", lambda: workspace)
    sender = _peer("sender-id", "sender-codex")
    target = _peer("orch-id", "orchestrator-claude-code", role=PeerRole.ORCHESTRATOR)
    transport = SimpleNamespace(
        send_notify=AsyncMock(return_value=NotifyTransportResult(status="sent")),
    )

    service = _service(config=Config(), sender=sender, target=target, transport_router=transport)
    await service.notify_result(
        from_peer=sender.peer_id,
        to_peer=target.peer_id,
        text="Completely unrelated quantum sandwich wording.",
        bypass_circle=True,
    )

    envelope = transport.send_notify.await_args.args[0]
    assert envelope.text == "Completely unrelated quantum sandwich wording."
