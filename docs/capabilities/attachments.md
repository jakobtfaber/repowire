# Attachments

## What it does

Attachments let dashboard and Telegram users send files or images to agents through the daemon. The receiving message includes a local file path that the target agent can read with its normal tools.

## Where available

- Dashboard compose bar.
- Telegram photos and documents.
- Relay tunnel for dashboard uploads when remote access is enabled.
- Slack currently routes text control messages; file relay is not part of the Slack surface.

## Limits

Uploads are limited to 10 MB and retained for 24 hours. Files are stored under `~/.repowire/attachments/` and cleaned up after the retention window. Agents receive paths; Repowire does not force a runtime to inspect the file.

The daemon accepts uploads at `POST /attachments` and serves downloads through `GET /attachments/{id}`. When the dashboard is reached through relay, upload payloads are tunneled back to the local daemon before the target agent sees the attachment path.

## Related

- [Guide: send files and images](../guides/send-files.md)
- [Dashboard](dashboard.md)
- [Telegram](telegram.md)
- [HTTP API](../reference/http-api.md)
