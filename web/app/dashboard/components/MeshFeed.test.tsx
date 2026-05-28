import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { MeshFeed } from "./MeshFeed";
import type { Event, Peer } from "../types";

const PEER: Peer = {
  peer_id: "peer-1",
  name: "alice",
  display_name: "alice",
  status: "online",
  machine: "host",
  path: "/tmp/alice",
  circle: "default",
};

describe("MeshFeed", () => {
  beforeEach(() => {
    Element.prototype.scrollIntoView = vi.fn();
  });

  it("uses apiBase for attachment download links", () => {
    const event: Event = {
      id: "event-1",
      type: "notification",
      timestamp: "2025-01-01T00:00:00Z",
      from: "alice",
      to: "bob",
      text: "see file",
      attachments: [{
        id: "att-123",
        filename: "diagram.png",
      }],
    };

    render(
      <MeshFeed
        events={[event]}
        peers={[PEER]}
        apiBase="http://daemon.test"
        onPickPeer={vi.fn()}
      />,
    );

    expect(screen.getByRole("link", { name: /diagram\.png/i })).toHaveAttribute(
      "href",
      "http://daemon.test/attachments/att-123",
    );
  });

  it("renders missing event actors as inert text", () => {
    const event: Event = {
      id: "event-unknown",
      type: "notification",
      timestamp: "2025-01-01T00:00:00Z",
      text: "system event",
    };

    render(
      <MeshFeed
        events={[event]}
        peers={[PEER]}
        apiBase="http://daemon.test"
        onPickPeer={vi.fn()}
      />,
    );

    expect(screen.getByText("unknown")).toBeInTheDocument();
    expect(screen.getByText("—")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "unknown" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "—" })).not.toBeInTheDocument();
  });

  it("keeps real peer actors clickable", () => {
    const event: Event = {
      id: "event-peer",
      type: "notification",
      timestamp: "2025-01-01T00:00:00Z",
      from: "alice",
      to: "bob",
      text: "hello",
    };
    const onPickPeer = vi.fn();

    render(
      <MeshFeed
        events={[event]}
        peers={[PEER]}
        apiBase="http://daemon.test"
        onPickPeer={onPickPeer}
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: "alice" }));

    expect(onPickPeer).toHaveBeenCalledWith(PEER);
  });

  it("renders bare ack events with a useful summary", () => {
    const event: Event = {
      id: "event-ack",
      type: "ack",
      timestamp: "2025-01-01T00:00:00Z",
      from: "alice",
      to: "bob",
      correlation_id: "ask-123",
      status: "success",
      delivered: false,
      has_message: false,
      has_attachments: false,
    };

    render(
      <MeshFeed
        events={[event]}
        peers={[PEER]}
        apiBase="http://daemon.test"
        onPickPeer={vi.fn()}
      />,
    );

    expect(screen.getByText("ack #ask-123 closed")).toBeInTheDocument();
    expect(screen.getByText("ack")).toBeInTheDocument();
  });

  it("renders ack reply event text when present", () => {
    const event: Event = {
      id: "event-ack-reply",
      type: "ack",
      timestamp: "2025-01-01T00:00:00Z",
      from: "alice",
      to: "bob",
      correlation_id: "ask-456",
      text: "fixed in commit abc",
      status: "success",
      delivered: true,
      has_message: true,
      has_attachments: false,
    };

    render(
      <MeshFeed
        events={[event]}
        peers={[PEER]}
        apiBase="http://daemon.test"
        onPickPeer={vi.fn()}
      />,
    );

    expect(screen.getByText("fixed in commit abc")).toBeInTheDocument();
  });

  it("renders peer reaped events without unknown actor buttons", () => {
    const event: Event = {
      id: "event-reaped",
      type: "peer_reaped",
      timestamp: "2025-01-01T00:00:00Z",
      peer_id: "peer-old",
      display_name: "old-codex",
      backend: "codex",
      path: "/repo/old",
      reason: "offline_ttl",
    };

    render(
      <MeshFeed
        events={[event]}
        peers={[PEER]}
        apiBase="http://daemon.test"
        onPickPeer={vi.fn()}
      />,
    );

    expect(screen.getByText(/reaped/)).toBeInTheDocument();
    expect(screen.getByText("old-codex")).toBeInTheDocument();
    expect(screen.getByText(/codex .* offline_ttl/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /unknown/i })).not.toBeInTheDocument();
  });
});
