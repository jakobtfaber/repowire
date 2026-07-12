#!/usr/bin/env bun
/**
 * Repowire Channel — Native Claude Code transport.
 *
 * Replaces hooks + tmux injection with a direct MCP channel.
 * Delivers messages to Claude Code natively via channel notifications;
 * Claude replies via the `reply` tool instead of transcript scraping.
 *
 * The daemon-facing side (WS connect, frame dispatch, correlation tracking)
 * lives in DaemonSession; this file is the Claude adapter: it maps inbound
 * messages onto channel notifications and MCP tools.
 */

import { Server } from "@modelcontextprotocol/sdk/server/index.js";
import { StdioServerTransport } from "@modelcontextprotocol/sdk/server/stdio.js";
import {
  ListToolsRequestSchema,
  CallToolRequestSchema,
} from "@modelcontextprotocol/sdk/types.js";
import { z } from "zod";
import { DaemonSession } from "./daemon-session.js";

// -- Daemon session --

const session = new DaemonSession();

// -- MCP Server --

const peerContext = await session.fetchPeerContext();

const mcp = new Server(
  { name: "repowire", version: "0.6.0" },
  {
    capabilities: {
      experimental: {
        "claude/channel": {},
        "claude/channel/permission": {},
      },
      tools: {},
    },
    instructions: [
      "Repowire mesh messages arrive as <channel source=\"repowire\" from_peer=\"...\" msg_type=\"...\">.",
      "For queries (msg_type=\"query\"), reply using the reply tool with the correlation_id from the tag.",
      "For asks (msg_type=\"ask\"), the tag carries correlation_id. Use the ack tool: ack(correlation_id) for bare close, ack(correlation_id, message) to deliver a reply to the original asker.",
      "For notifications (msg_type=\"notify\"), act on them directly.",
      "Messages from @dashboard or @telegram are from the human user — treat as direct instructions.",
      peerContext,
    ]
      .filter(Boolean)
      .join("\n"),
  }
);

// -- Deliver inbound messages to Claude via channel notification --

session.connect(async (msg) => {
  const meta: Record<string, string> = {
    from_peer: msg.fromPeer,
    msg_type: msg.type,
  };

  if ((msg.type === "query" || msg.type === "ask") && msg.correlationId) {
    meta.correlation_id = msg.correlationId;
  }
  if (msg.type === "ask" && msg.replyTo) {
    meta.reply_to = msg.replyTo;
  }

  await mcp.notification({
    method: "notifications/claude/channel",
    params: {
      content: msg.content,
      meta,
    },
  });
});

// -- Reply tool --

mcp.setRequestHandler(ListToolsRequestSchema, async () => ({
  tools: [
    {
      name: "reply",
      description:
        "Reply to a repowire query. Pass the correlation_id from the <channel> tag.",
      inputSchema: {
        type: "object" as const,
        properties: {
          correlation_id: {
            type: "string",
            description: "The correlation_id from the query's <channel> tag",
          },
          text: {
            type: "string",
            description: "Your response text",
          },
        },
        required: ["correlation_id", "text"],
      },
    },
  ],
}));

const ReplyArgs = z.object({
  correlation_id: z.string(),
  text: z.string(),
});

mcp.setRequestHandler(CallToolRequestSchema, async (req) => {
  if (req.params.name === "reply") {
    const { correlation_id, text } = ReplyArgs.parse(req.params.arguments);

    if (session.sendResponse(correlation_id, text)) {
      return { content: [{ type: "text" as const, text: "Reply sent." }] };
    }
    return {
      content: [
        { type: "text" as const, text: "Error: not connected to daemon." },
      ],
    };
  }
  throw new Error(`Unknown tool: ${req.params.name}`);
});

// -- Permission relay --

const PermissionRequestSchema = z.object({
  method: z.literal("notifications/claude/channel/permission_request"),
  params: z.object({
    request_id: z.string(),
    tool_name: z.string(),
    description: z.string(),
    input_preview: z.string(),
  }),
});

mcp.setNotificationHandler(PermissionRequestSchema, async ({ params }) => {
  // Forward permission prompt to daemon for relay to Telegram/dashboard
  session.sendNotify(
    `🔐 Permission request: ${params.tool_name}\n` +
      `${params.description}\n\n` +
      `Reply "yes ${params.request_id}" or "no ${params.request_id}"`
  );
});

// -- Connect --

await mcp.connect(new StdioServerTransport());
