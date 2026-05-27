# Run the Remote Dashboard

## Goal

Reach your local dashboard remotely without opening inbound ports to your machine.

## Steps

Enable the hosted relay:

```bash
repowire setup --relay
repowire serve
```

Then open:

```text
https://repowire.io/dashboard
```

The daemon connects outbound to the relay. Dashboard HTTP, SSE, and WebSocket traffic tunnel back through that connection.

## Verify

Run `repowire status` and confirm relay is enabled. If remote access fails, check [Relay key rotation](../troubleshooting/relay-keys.md) and [Operations: relay](../operations/relay.md).

## Related

- [Capabilities: relay access](../capabilities/relay-access.md)
- [Operations: auth and security](../operations/security.md)
