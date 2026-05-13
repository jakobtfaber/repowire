import pytest
from unittest.mock import AsyncMock, MagicMock
from repowire.daemon.peer_registry import PeerRegistry
from repowire.daemon.message_router import MessageRouter
from repowire.protocol.peers import Peer, PeerStatus

@pytest.fixture
def mock_router():
    router = MagicMock(spec=MessageRouter)
    router.send_notification = AsyncMock()
    router.send_ask = AsyncMock()
    return router

@pytest.fixture
def registry(mock_router):
    reg = PeerRegistry(transport=MagicMock(), query_tracker=MagicMock())
    reg._router = mock_router
    return reg

@pytest.mark.asyncio
async def test_append_client_hint_dashboard(registry, mock_router):
    # Setup a target peer
    target = Peer(peer_id="target-id", display_name="target", path="/tmp", machine="host")
    target.status = PeerStatus.ONLINE
    registry._peers["target"] = target
    
    await registry.notify(from_peer="dashboard", to_peer="target", text="hello")
    
    expected_text = "hello\n(from @dashboard - reply naturally, dashboard sees your response automatically)"
    mock_router.send_notification.assert_called_once_with(
        from_peer="dashboard",
        to_session_id="target-id",
        to_peer_name="target",
        text=expected_text
    )

@pytest.mark.asyncio
async def test_append_client_hint_telegram(registry, mock_router):
    target = Peer(peer_id="target-id", display_name="target", path="/tmp", machine="host")
    target.status = PeerStatus.ONLINE
    registry._peers["target"] = target
    
    await registry.deliver_ask(from_peer="telegram", to_peer="target", text="hello", correlation_id="cid")
    
    expected_text = "hello\n(from @telegram - reply naturally, your response is delivered back to my phone)"
    mock_router.send_ask.assert_called_once_with(
        from_peer="telegram",
        to_session_id="target-id",
        to_peer_name="target",
        correlation_id="cid",
        text=expected_text,
        reply_to=None
    )

@pytest.mark.asyncio
async def test_does_not_duplicate_hint(registry, mock_router):
    target = Peer(peer_id="target-id", display_name="target", path="/tmp", machine="host")
    target.status = PeerStatus.ONLINE
    registry._peers["target"] = target
    
    hint = "(from @slack - reply naturally, your response is delivered back to Slack)"
    text = f"hello\n{hint}"
    
    await registry.notify(from_peer="slack", to_peer="target", text=text)
    
    # Should not add another hint
    mock_router.send_notification.assert_called_once_with(
        from_peer="slack",
        to_session_id="target-id",
        to_peer_name="target",
        text=text
    )
