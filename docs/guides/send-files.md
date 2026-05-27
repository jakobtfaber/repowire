# Send Files and Images

## Goal

Send a local file or image through the dashboard or Telegram so an agent can read it from disk.

## Steps

1. Upload a file through the dashboard compose bar or send a Telegram photo/document.
2. Repowire stores the attachment under the local attachment directory.
3. The receiving message includes the local path.
4. Ask the target agent to read the file path with its normal file-reading tool.

## Limits

Uploads are intended for short-lived context. The daemon enforces a 10 MB upload limit and cleans old attachments after 24 hours. Files live under `~/.repowire/attachments/`.

Dashboard and Telegram support attachments. Slack currently routes text control messages, not file relay. When dashboard uploads go through the hosted relay, the relay tunnels the upload to the local daemon and the agent still receives a local path.

## Related

- [Capabilities: attachments](../capabilities/attachments.md)
- [Dashboard](../capabilities/dashboard.md)
- [Telegram](../capabilities/telegram.md)
- [HTTP API](../reference/http-api.md)
