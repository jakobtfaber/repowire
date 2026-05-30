type IconName = "ask" | "local" | "agents" | "tmux" | "human" | "audit";

const secondary: { icon: IconName; title: string; body: string }[] = [
  {
    icon: "local",
    title: "Local by default",
    body: "A single daemon on port 8377 is the hub. The hosted relay is opt-in, and it's only there for browser and phone access.",
  },
  {
    icon: "agents",
    title: "Agent-agnostic",
    body: "Claude Code, Codex, Gemini CLI, OpenCode, Pi — anything that can hit an HTTP endpoint can register as a peer.",
  },
  {
    icon: "tmux",
    title: "Tmux-native",
    body: "Sessions are named, persistent, and steerable. Repowire never owns the agent's process; it just helps them talk.",
  },
  {
    icon: "human",
    title: "Human in the loop",
    body: "Watch the mesh from the dashboard. Pause an agent, redirect a question, or step in mid-conversation.",
  },
  {
    icon: "audit",
    title: "Audit trail by default",
    body: "Every ask / ack / notify is logged with provenance. Replay a run, export a transcript, or git-commit it.",
  },
];

export default function Features() {
  return (
    <section className="features" id="features">
      <div className="section-head">
        <span className="eyebrow">What Repowire does</span>
        <h2>Built around one idea: agents that can ask.</h2>
      </div>

      <div className="feature-hero">
        <div className="feature-hero-copy">
          <div className="feature-icon feature-icon-lg">
            <FeatureIcon name="ask" />
          </div>
          <h3>Ask across repos</h3>
          <p>
            Send a question to the peer that&apos;s already working in another checkout. It answers
            from its live tree and sends back an explicit ack — never a vibes-based reply, never a
            copy-paste handoff.
          </p>
        </div>
        <div className="feature-hero-visual">
          <div className="mesh-log-rows">
            <AskRow t="14:02" from="@backend" verb="ask" to="@frontend" body="What's the auth response shape from /me?" />
            <AskRow t="14:02" from="@frontend" verb="ack" to="@backend" body="{ user, session } — no nested wrapper." />
            <AskRow t="14:04" from="@db-migrations" verb="notify" body="Migration 0042 applied." />
          </div>
        </div>
      </div>

      <div className="feature-grid">
        {secondary.map((it) => (
          <div className="feature" key={it.title}>
            <div className="feature-icon">
              <FeatureIcon name={it.icon} />
            </div>
            <h3>{it.title}</h3>
            <p>{it.body}</p>
          </div>
        ))}
      </div>
    </section>
  );
}

function AskRow({
  t,
  from,
  verb,
  to,
  body,
}: {
  t: string;
  from: string;
  verb: "ask" | "ack" | "notify";
  to?: string;
  body: string;
}) {
  const verbClass = { ask: "v-ask", ack: "v-ack", notify: "v-notify" }[verb];
  return (
    <div className="mesh-log-row">
      <div className="mesh-log-when">{t}</div>
      <div>
        <div className="mesh-log-head-row">
          <span className="mesh-log-peer">{from}</span>
          <span className={`verb-pill ${verbClass}`}>{verb}</span>
          {to && (
            <>
              <span className="mesh-log-arrow">→</span>
              <span className="mesh-log-peer">{to}</span>
            </>
          )}
        </div>
        <div className="mesh-log-body">{body}</div>
      </div>
    </div>
  );
}

function FeatureIcon({ name }: { name: IconName }) {
  const stroke = {
    stroke: "currentColor",
    strokeWidth: 1.75,
    fill: "none",
    strokeLinecap: "round" as const,
    strokeLinejoin: "round" as const,
  };
  const props = { width: 20, height: 20, viewBox: "0 0 24 24", ...stroke };
  switch (name) {
    case "ask":
      return (
        <svg {...props}>
          <path d="M16 18l6-6-6-6M8 6l-6 6 6 6" />
        </svg>
      );
    case "local":
      return (
        <svg {...props}>
          <rect x="3" y="4" width="18" height="12" rx="2" />
          <line x1="8" y1="20" x2="16" y2="20" />
          <line x1="12" y1="16" x2="12" y2="20" />
        </svg>
      );
    case "agents":
      return (
        <svg {...props}>
          <circle cx="9" cy="9" r="2.5" />
          <circle cx="17" cy="13" r="2" />
          <circle cx="7" cy="17" r="2" />
          <line x1="10.5" y1="10.5" x2="15.5" y2="12.5" />
          <line x1="8" y1="15" x2="10" y2="11" />
        </svg>
      );
    case "tmux":
      return (
        <svg {...props}>
          <rect x="3" y="3" width="18" height="18" rx="2" />
          <line x1="12" y1="3" x2="12" y2="21" />
          <line x1="3" y1="12" x2="12" y2="12" />
        </svg>
      );
    case "human":
      return (
        <svg {...props}>
          <circle cx="12" cy="8" r="4" />
          <path d="M4 21a8 8 0 0 1 16 0" />
        </svg>
      );
    case "audit":
      return (
        <svg {...props}>
          <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z" />
          <polyline points="14 2 14 8 20 8" />
          <line x1="8" y1="13" x2="16" y2="13" />
          <line x1="8" y1="17" x2="13" y2="17" />
        </svg>
      );
  }
}
