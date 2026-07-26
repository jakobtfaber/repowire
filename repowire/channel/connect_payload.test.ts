import { describe, expect, test } from "bun:test";

import {
  ChannelIdentity,
  buildConnectPayload,
  buildPeerFetchInit,
} from "./connect_payload.js";

describe("buildConnectPayload", () => {
  test("proves a claimed durable peer with the Claude parent process", () => {
    expect(
      buildConnectPayload({
        displayName: "channel",
        circle: "default",
        projectPath: "/tmp/repo",
        claimedPeerId: "repow-default-claude123",
        agentPid: 4242,
        authToken: "",
      }),
    ).toEqual({
      type: "connect",
      display_name: "channel",
      circle: "default",
      backend: "claude-code",
      path: "/tmp/repo",
      peer_id: "repow-default-claude123",
      agent_pid: 4242,
    });
  });

  test("does not attach process proof to an unclaimed channel identity", () => {
    expect(
      buildConnectPayload({
        displayName: "channel",
        circle: "default",
        projectPath: "/tmp/repo",
        claimedPeerId: "",
        agentPid: 4242,
        authToken: "secret",
      }),
    ).toEqual({
      type: "connect",
      display_name: "channel",
      circle: "default",
      backend: "claude-code",
      path: "/tmp/repo",
      auth_token: "secret",
    });
  });

  test("does not misrepresent invalid parent process proof", () => {
    expect(
      buildConnectPayload({
        displayName: "channel",
        circle: "default",
        projectPath: "/tmp/repo",
        claimedPeerId: "repow-default-claude123",
        agentPid: 0,
        authToken: "",
      }),
    ).toEqual({
      type: "connect",
      display_name: "channel",
      circle: "default",
      backend: "claude-code",
      path: "/tmp/repo",
      peer_id: "repow-default-claude123",
    });
  });
});

describe("ChannelIdentity", () => {
  test("reconnect claims the peer ID assigned to the first unclaimed connection", () => {
    const identity = new ChannelIdentity("");
    const options = {
      displayName: "channel",
      circle: "default",
      projectPath: "/tmp/repo",
      agentPid: 4242,
      authToken: "",
    };

    expect(identity.buildConnectPayload(options)).toEqual({
      type: "connect",
      display_name: "channel",
      circle: "default",
      backend: "claude-code",
      path: "/tmp/repo",
    });

    identity.rememberAssignedPeerId("repow-default-assigned123");

    expect(identity.buildConnectPayload(options)).toEqual({
      type: "connect",
      display_name: "channel",
      circle: "default",
      backend: "claude-code",
      path: "/tmp/repo",
      peer_id: "repow-default-assigned123",
      agent_pid: 4242,
    });
  });
});

describe("buildPeerFetchInit", () => {
  test("authenticates the peers request when a daemon token exists", () => {
    expect(buildPeerFetchInit("secret-token")).toEqual({
      headers: { Authorization: "Bearer secret-token" },
    });
    expect(buildPeerFetchInit("")).toBeUndefined();
  });
});
