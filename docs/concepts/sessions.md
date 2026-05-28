# Sessions

Sessions are the durable work context Repowire is moving toward. Peers are the running agent processes connected right now; sessions are the longer-lived unit that can accumulate timeline events, transcript history, and eventually shared controls such as resume, schedule, approvals, and backend selection.

## Why it exists

Peer-first routing works well for live asks and notifications, but long-running work needs a stable target even when the executor disconnects or restarts. The session-native direction keeps peer routing compatible while giving dashboard and future control surfaces a durable object to show and command.

## Current behavior

The stable surface is still peer-oriented: peers, circles, asks, notifications, broadcasts, schedules, and jobs. The dashboard already presents selected peer/session timeline views where transcript history is available, and the first session-targeted routes resolve session bindings to live executors or explicit resume-capability status.

All Repowire sessions should be treated as durable context. The dashboard's
active-session nudge panel is narrower: it can only send a nudge when that
durable session currently has a running agent attached. If the session has
history but no running agent, the timeline can still be useful, but nudge
buttons are disabled until there is an active agent or a future resume path.

## Related

- [Session-native roadmap](session-native-roadmap.md)
- [Peer identity lifecycle](peer-identity-lifecycle.md)
- [Operations: architecture](../operations/architecture.md)
