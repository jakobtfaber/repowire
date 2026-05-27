import httpx
import pytest

from repowire.relay.auth import APIKey, _token_registry, register_token, validate_api_key
from repowire.relay.server import create_app


class TestRelayAuth:
    def setup_method(self):
        _token_registry.clear()

    def test_register_token(self):
        api_key = register_token("user1")
        assert api_key.key.startswith("rw_")
        assert api_key.user_id == "user1"

    def test_register_returns_same_for_same_user(self):
        k1 = register_token("user1")
        k2 = register_token("user1")
        assert k1.key == k2.key

    def test_register_different_users_get_different_keys(self):
        k1 = register_token("user1")
        k2 = register_token("user2")
        assert k1.key != k2.key

    def test_validate_registered_key(self):
        registered = register_token("user1")
        validated = validate_api_key(registered.key)
        assert validated is not None
        assert validated.user_id == "user1"
        assert validated.key == registered.key

    def test_validate_unknown_key_auto_registers(self):
        result = validate_api_key("rw_someunknownbutwellformedtoken")
        assert result is not None
        assert result.key == "rw_someunknownbutwellformedtoken"

    def test_validate_wrong_prefix(self):
        result = validate_api_key("bad_prefix_key")
        assert result is None

    def test_validate_too_short(self):
        result = validate_api_key("rw_short")
        assert result is None

    def test_api_key_model(self):
        key = APIKey(key="rw_test123", user_id="user1")
        assert key.key == "rw_test123"
        assert key.user_id == "user1"

    def test_token_length(self):
        api_key = register_token("user1")
        # rw_ prefix + 32 chars of base64url
        assert len(api_key.key) > 20


class TestRelayLanding:
    @pytest.mark.asyncio
    async def test_auth_invalid_key_returns_landing_error(self):
        app = create_app()
        transport = httpx.ASGITransport(app=app)
        async with httpx.AsyncClient(transport=transport, base_url="https://relay.test") as client:
            response = await client.post(
                "/auth",
                data={"token": "not-a-relay-key"},
                follow_redirects=True,
            )

        assert response.status_code == 200
        assert "That relay key was not recognized." in response.text
        assert '{"detail"' not in response.text

    @pytest.mark.asyncio
    async def test_auth_missing_key_returns_landing_error(self):
        app = create_app()
        transport = httpx.ASGITransport(app=app)
        async with httpx.AsyncClient(transport=transport, base_url="https://relay.test") as client:
            response = await client.post("/auth", data={"token": ""}, follow_redirects=True)

        assert response.status_code == 200
        assert "Enter your relay key." in response.text
