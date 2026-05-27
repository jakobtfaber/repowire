# Use the Dashboard

## Goal

Use the browser dashboard to inspect peers, follow session timelines, and send messages without leaving the local daemon.

## Before you start

Start the daemon:

```bash
repowire serve
```

Then open:

```text
http://localhost:8377/dashboard
```

## Steps

1. Confirm the peers you expect are online.
2. Select a peer or session to inspect its timeline.
3. Use the compose bar to send a message or attach a file.
4. Watch chat turns, tool calls, mesh events, and replies in the timeline.

## Verify

Run `repowire peer list` in a terminal and compare the peer list with the dashboard. If the daemon is not reachable, use [Daemon unreachable](../troubleshooting/daemon.md).

## Related

- [Capabilities: dashboard](../capabilities/dashboard.md)
- [Run the remote dashboard](run-remote-dashboard.md)
