"""Shared daemon authentication for local Repowire clients."""

from __future__ import annotations

import os


def daemon_auth_token() -> str | None:
    """Return the daemon bearer token, preferring the process environment."""
    token = os.environ.get("REPOWIRE_AUTH_TOKEN")
    if token:
        return token
    try:
        from repowire.config.models import load_config

        return load_config().daemon.auth_token
    except Exception:
        return None


def daemon_auth_headers() -> dict[str, str]:
    """Return an Authorization header when daemon authentication is configured."""
    token = daemon_auth_token()
    return {"Authorization": f"Bearer {token}"} if token else {}
