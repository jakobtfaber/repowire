# Running Tests

## Quality gates

```bash
pytest
ruff check repowire/
uv run ty check repowire/
```

CI runs the same core gates.

## Notes

- Route tests use `httpx.AsyncClient` with `ASGITransport`.
- WebSocket tests use `httpx-ws` with `ASGIWebSocketTransport`.
- Mock `subprocess.Popen` in session-handler tests so the WebSocket hook does not leak to a live daemon.
- Reinstall the package after hook changes because hooks run from the installed package.

## Related

- [Development setup](development-setup.md)
- [Pre-PR hygiene](pre-pr-hygiene.md)
