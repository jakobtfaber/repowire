# Use Slack

## Goal

Control Repowire from a Slack workspace through the Socket Mode bot.

## Before you start

Create Slack bot and app tokens, then decide which channel should be able to reach the mesh.

## Steps

```bash
SLACK_BOT_TOKEN=xoxb-... SLACK_APP_TOKEN=xapp-... SLACK_CHANNEL_ID=C... repowire slack start
```

Use the Block Kit peer picker to select a target peer. Normal messages route through the sticky selection; notify/FYI commands stay fire-and-forget.

## Verify

Confirm the bot is online in Slack and run `repowire peer list` locally. The Slack peer should appear as a service peer.

## Related

- [Capabilities: Slack](../capabilities/slack.md)
- [Pattern: mobile mesh management](../patterns/mobile-mesh.md)
