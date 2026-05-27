# Sessions

Sessions are the durable work context Repowire is moving toward. Peers are the live executors connected right now; sessions are the longer-lived unit that can accumulate timeline events, transcript history, and eventually shared controls such as resume, schedule, approvals, and backend selection.

## Why it exists

Peer-first routing works well for live asks and notifications, but long-running work needs a stable target even when the executor disconnects or restarts. The session-native direction keeps peer routing compatible while giving dashboard and future control surfaces a durable object to show and command.

## Current behavior

The stable surface is still peer-oriented: peers, circles, asks, notifications, broadcasts, schedules, and jobs. The dashboard already presents selected peer/session timeline views where transcript history is available, and the first session-targeted routes resolve session bindings to live executors or explicit resume-capability status.

## Related

- [Session-native roadmap](session-native-roadmap.md)
- [Peer identity lifecycle](peer-identity-lifecycle.md)
- [Operations: architecture](../operations/architecture.md)
