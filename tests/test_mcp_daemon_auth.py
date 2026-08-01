from types import SimpleNamespace
from unittest.mock import AsyncMock, MagicMock

import pytest

import repowire.mcp.server as server


def mock_http_client():
    client = MagicMock()
    client.get = AsyncMock()
    response = MagicMock()
    response.json.return_value = {"ok": True}
    client.get.return_value = response
    return client


@pytest.fixture(autouse=True)
def reset_mcp_context():
    server.reset_mcp_context()
    yield
    server.reset_mcp_context()


async def test_stdio_daemon_request_uses_environment_bearer_token(monkeypatch):
    client = mock_http_client()
    monkeypatch.setattr(server, "_http_client", client)
    monkeypatch.setenv("REPOWIRE_AUTH_TOKEN", "stdio-secret")

    await server.daemon_request("GET", "/peers")

    client.get.assert_awaited_once_with(
        f"{server.DAEMON_URL}/peers",
        params=None,
        headers={"Authorization": "Bearer stdio-secret"},
    )


async def test_stdio_daemon_request_falls_back_to_config_bearer_token(monkeypatch):
    client = mock_http_client()
    monkeypatch.setattr(server, "_http_client", client)
    monkeypatch.delenv("REPOWIRE_AUTH_TOKEN", raising=False)
    monkeypatch.setattr(
        server,
        "load_config",
        lambda: SimpleNamespace(daemon=SimpleNamespace(auth_token="config-secret")),
    )

    await server.daemon_request("GET", "/peers")

    assert client.get.await_args.kwargs["headers"] == {
        "Authorization": "Bearer config-secret"
    }


async def test_stdio_daemon_request_reloads_config_token(monkeypatch):
    client = mock_http_client()
    monkeypatch.setattr(server, "_http_client", client)
    monkeypatch.delenv("REPOWIRE_AUTH_TOKEN", raising=False)
    config = SimpleNamespace(daemon=SimpleNamespace(auth_token="first-secret"))
    monkeypatch.setattr(server, "load_config", lambda: config)

    await server.daemon_request("GET", "/peers")
    config.daemon.auth_token = "rotated-secret"
    await server.daemon_request("GET", "/peers")

    first_call, second_call = client.get.await_args_list
    assert first_call.kwargs["headers"] == {
        "Authorization": "Bearer first-secret"
    }
    assert second_call.kwargs["headers"] == {
        "Authorization": "Bearer rotated-secret"
    }


async def test_http_mcp_daemon_request_keeps_configured_http_token(monkeypatch):
    client = mock_http_client()
    monkeypatch.setattr(server, "_http_client", client)
    monkeypatch.setenv("REPOWIRE_AUTH_TOKEN", "stdio-secret")
    server.configure_http_mcp_context(auth_token="http-secret")

    await server.daemon_request("GET", "/peers")

    assert client.get.await_args.kwargs["headers"] == {
        "Authorization": "Bearer http-secret"
    }
