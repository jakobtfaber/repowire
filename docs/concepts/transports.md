# Transports

Transports are the ways messages reach a runtime. They are deliberately below the user-facing routing model: all peers still use the same `ask`, `ack`, `notify_peer`, and `broadcast` semantics.

Glossary: hooks observe lifecycle, status, chat turns, and reminders; MCP is the outbound agent tool surface; WebSocket plus tmux injection is the default live inbound delivery mechanism for hook-based runtimes.

## Current transports

- Hooks + MCP for Claude Code, Codex, and Gemini. Hooks handle lifecycle, chat extraction, and Stop-hook reminders; MCP handles outbound commands; live inbound messages are delivered over the daemon WebSocket path and injected into the runtime's tmux pane by default.
- Plugin + WebSocket for OpenCode.
- Extension path for Pi.
- Experimental channel/ACP transport for Claude Code.
- Relay tunnel for remote dashboard and cross-machine traffic.

## Why it exists

Each agent runtime exposes different hooks, event names, and message-delivery affordances. Repowire normalizes those into a peer registry, transport router, and message lifecycle so higher-level tools do not need to know which runtime is on the other side.

## Related

- [Operations: transports](../operations/transports.md)
- [Hook payloads](../reference/hook-payloads.md)
- [Message types](message-types.md)
- [MCP tools](../reference/mcp-tools.md)
