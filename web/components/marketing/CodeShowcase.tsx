type Verb = "ask" | "ack" | "notify" | "broadcast";

const verbClass: Record<Verb, string> = {
  ask: "v-ask",
  ack: "v-ack",
  notify: "v-notify",
  broadcast: "v-broadcast",
};

const phoneEvents: { t: string; from: string; verb: Verb; to?: string; body: string }[] = [
  { t: "2m", from: "@backend", verb: "ask", to: "@frontend", body: "auth response shape?" },
  { t: "2m", from: "@frontend", verb: "ack", to: "@backend", body: "{ user, session }" },
  { t: "14m", from: "@db-migrations", verb: "notify", body: "Migration 0042 applied." },
  { t: "22m", from: "@qa", verb: "broadcast", body: "Nightly green. Safe to ship." },
];

export default function CodeShowcase() {
  return (
    <section className="showcase">
      <div className="section-head">
        <span className="eyebrow">Two surfaces, one mesh</span>
        <h2>Live in your terminal. Steerable from anywhere.</h2>
        <p className="section-sub">
          The CLI is the source of truth. The dashboard is the same data, rendered for a thumb.
        </p>
      </div>
      <div className="showcase-grid">
        <div className="showcase-panel terminal-panel">
          <div className="term-head">
            <div className="term-dots">
              <span />
              <span />
              <span />
            </div>
            <div className="term-title">repowire — tmux:main</div>
          </div>
          <pre className="term-body">
            <div>
              <span className="t-d">$ </span>
              <span className="t-c">repowire peer ls</span>
            </div>
            <div className="t-m">NAME            AGENT          STATUS    LAST SEEN</div>
            <div className="t-row">backend         claude-code    online    4s</div>
            <div className="t-row">frontend        codex          online    22s</div>
            <div className="t-row t-stale">db-migrations   gemini-cli     stale     4m</div>
            <div className="t-row t-off">e2e-runner      opencode       offline   2h</div>
            <div className="term-blank">&nbsp;</div>
            <div>
              <span className="t-d">$ </span>
              <span className="t-c">repowire</span>
              <span> ask </span>
              <span className="t-arg">@frontend</span>
              <span> &quot;auth response shape?&quot;</span>
            </div>
            <div className="t-g">→ sent · waiting on ack</div>
            <div>
              <span className="t-g">← @frontend ack</span>
              <span> {`{ user, session }`} — no wrapper.</span>
            </div>
          </pre>
        </div>
        <div className="showcase-panel device-panel">
          <div className="device-frame">
            <div className="device-notch" />
            <div className="device-screen">
              <div className="phone-top">
                <span style={{ fontWeight: 600, fontSize: 15 }}>Mesh</span>
                <div style={{ display: "flex", gap: 8, alignItems: "center" }}>
                  <span className="badge-online">
                    <span className="dot" />4 online
                  </span>
                </div>
              </div>
              <div className="phone-tabs">
                <span className="phone-tab active">Feed</span>
                <span className="phone-tab">Peers</span>
                <span className="phone-tab">Ask</span>
              </div>
              <div className="phone-feed">
                {phoneEvents.map((ev, i) => (
                  <PhoneEvent key={i} {...ev} />
                ))}
              </div>
            </div>
          </div>
        </div>
      </div>
    </section>
  );
}

function PhoneEvent({ t, from, verb, to, body }: { t: string; from: string; verb: Verb; to?: string; body: string }) {
  return (
    <div className="phone-ev">
      <div className="phone-ev-head">
        <span className="phone-ev-peer">{from}</span>
        <span className={`verb-pill ${verbClass[verb]}`}>{verb}</span>
        {to && <span className="phone-ev-to">{to}</span>}
        <span className="phone-ev-when">{t}</span>
      </div>
      <div className="phone-ev-body">{body}</div>
    </div>
  );
}
