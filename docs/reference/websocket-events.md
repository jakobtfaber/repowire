# WebSocket Events

Repowire peers use WebSocket delivery for live messages and daemon events. The daemon remains the routing hub; transports decide how to present those messages to a runtime.

## Message families

- `ask` — tracked question that must be closed with `ack`.
- `notify` — fire-and-forget message.
- `broadcast` — mesh-wide announcement.
- Dashboard events — timeline, peer, chat-turn, tool-call, and operational events.

## Compatibility

The exact event payloads are transport-facing internals unless documented by a client or MCP tool. Prefer MCP tools for agent actions and the Python client for programmatic daemon access.

## Related

- [Message types](../concepts/message-types.md)
- [MCP tools](mcp-tools.md)
- [Operations: transports](../operations/transports.md)
