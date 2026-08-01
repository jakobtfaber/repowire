from __future__ import annotations

import pytest

from repowire.daemon.deps import cleanup_deps
from repowire.daemon.routes import asks, messages, peers
from tests.conftest import async_client_for, make_daemon_app

REMOTE_PEER = {
    "peer_id": "repow-remote-worker",
    "name": "remote-worker",
    "display_name": "remote-worker",
    "path": "/remote/project",
    "machine": "remote-host",
    "backend": "codex",
    "circle": "default",
    "role": "agent",
    "status": "online",
    "metadata": {"federation_daemon_id": "remote-daemon"},
}


class FakeRelay:
    connected = True
    daemon_id = "origin-daemon"

    def __init__(self) -> None:
        self.requests: list[tuple[str, str, str, dict | None]] = []

    async def remote_peers(self, params=None):
        return [REMOTE_PEER]

    async def find_remote_peer(self, identifier, *, circle=None):
        if identifier in (REMOTE_PEER["peer_id"], REMOTE_PEER["display_name"]):
            return REMOTE_PEER
        return None

    async def request(self, daemon_id, method, path, *, body=None, params=None, timeout=15):
        self.requests.append((daemon_id, method, path, body))
        if path == "/notify":
            return {
                "ok": True,
                "status": "sent",
                "delivery_state": "delivered",
                "delivered": True,
                "queued": False,
                "reason": "transport_delivered",
                "to_peer_id": REMOTE_PEER["peer_id"],
                "to_peer_name": REMOTE_PEER["display_name"],
            }
        return {"correlation_id": body.get("correlation_id") if body else None}


@pytest.fixture
async def federation_env(tmp_path):
    relay = FakeRelay()
    harness = make_daemon_app(
        tmp_path,
        [peers.router, messages.router, asks.router],
        state_overrides={"relay_client": relay},
    )
    async with async_client_for(harness.app) as client:
        registered = await client.post(
            "/peers",
            json={
                "peer_id": "repow-default-localasker",
                "name": "local-asker",
                "path": "/local/project",
                "machine": "local-host",
                "backend": "codex",
                "circle": "default",
            },
        )
        assert registered.status_code == 200, registered.text
        local_peer_id = registered.json()["peer_id"]
        yield client, harness, relay, local_peer_id
    cleanup_deps()


async def test_list_peers_merges_remote_daemon(federation_env):
    client, _harness, _relay, _local_peer_id = federation_env

    response = await client.get("/peers?status=online")

    assert response.status_code == 200
    remote = next(p for p in response.json()["peers"] if p["peer_id"] == REMOTE_PEER["peer_id"])
    assert remote["metadata"]["federation_daemon_id"] == "remote-daemon"


async def test_notify_routes_to_remote_daemon(federation_env):
    client, harness, relay, local_peer_id = federation_env

    response = await client.post(
        "/notify",
        json={
            "from_peer": local_peer_id,
            "to_peer": "remote-worker",
            "text": "hello across machines",
        },
    )

    assert response.status_code == 200
    assert response.json()["to_peer_id"] == REMOTE_PEER["peer_id"]
    daemon_id, method, path, body = relay.requests[-1]
    assert (daemon_id, method, path) == ("remote-daemon", "POST", "/notify")
    local_peer = await harness.registry.get_peer(local_peer_id)
    assert body["from_peer"] == local_peer.display_name
    assert body["to_peer"] == REMOTE_PEER["peer_id"]


async def test_ask_tracks_origin_and_routes_to_remote_daemon(federation_env):
    client, harness, relay, local_peer_id = federation_env

    response = await client.post(
        "/ask",
        json={
            "from_peer": local_peer_id,
            "to_peer": "remote-worker",
            "text": "please reply",
        },
    )

    assert response.status_code == 200
    cid = response.json()["correlation_id"]
    tracked = await harness.ask_tracker.get(cid)
    assert tracked is not None
    assert tracked.from_peer_id == local_peer_id
    assert tracked.to_peer_id == REMOTE_PEER["peer_id"]
    body = relay.requests[-1][3]
    assert body["correlation_id"] == cid
    assert body["origin_daemon_id"] == "origin-daemon"


async def test_federated_ack_returns_to_origin_daemon(tmp_path):
    relay = FakeRelay()
    relay.daemon_id = "remote-daemon"
    harness = make_daemon_app(
        tmp_path,
        [asks.router],
        state_overrides={"relay_client": relay},
    )
    cid = await harness.ask_tracker.register(
        from_peer_id="local-asker",
        from_peer_name="local-asker",
        to_peer_id=REMOTE_PEER["peer_id"],
        to_peer_name=REMOTE_PEER["display_name"],
        text="please reply",
        origin_daemon_id="origin-daemon",
    )

    async with async_client_for(harness.app) as client:
        response = await client.post(
            "/ack", json={"correlation_id": cid, "message": "done"},
        )

    assert response.status_code == 200
    assert relay.requests[-1][:3] == ("origin-daemon", "POST", "/ack")
    assert relay.requests[-1][3]["message"] == "done"
    tracked = await harness.ask_tracker.get(cid)
    assert tracked is not None and tracked.closed
    cleanup_deps()
