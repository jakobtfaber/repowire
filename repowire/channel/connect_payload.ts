export type ConnectPayloadOptions = {
  displayName: string;
  circle: string;
  projectPath: string;
  claimedPeerId: string;
  agentPid: number;
  authToken: string;
};

export type ManagedConnectPayloadOptions = Omit<
  ConnectPayloadOptions,
  "claimedPeerId"
>;

export function buildConnectPayload({
  displayName,
  circle,
  projectPath,
  claimedPeerId,
  agentPid,
  authToken,
}: ConnectPayloadOptions): Record<string, string | number> {
  const hasLiveProcessProof =
    Number.isSafeInteger(agentPid) && agentPid > 0;
  return {
    type: "connect",
    display_name: displayName,
    circle,
    backend: "claude-code",
    path: projectPath,
    ...(claimedPeerId
      ? {
          peer_id: claimedPeerId,
          ...(hasLiveProcessProof ? { agent_pid: agentPid } : {}),
        }
      : {}),
    ...(authToken ? { auth_token: authToken } : {}),
  };
}

export class ChannelIdentity {
  #peerId: string;

  constructor(initialPeerId: string) {
    this.#peerId = initialPeerId;
  }

  get peerId(): string | null {
    return this.#peerId || null;
  }

  rememberAssignedPeerId(peerId: string): void {
    if (peerId) this.#peerId = peerId;
  }

  buildConnectPayload(
    options: ManagedConnectPayloadOptions,
  ): Record<string, string | number> {
    return buildConnectPayload({
      ...options,
      claimedPeerId: this.#peerId,
    });
  }
}

export function buildPeerFetchInit(
  authToken: string,
): { headers: { Authorization: string } } | undefined {
  if (!authToken) return undefined;
  return { headers: { Authorization: `Bearer ${authToken}` } };
}
