export default function CodeShowcase() {
  return (
    <section className="showcase">
      <div className="section-head">
        <span className="eyebrow">The CLI</span>
        <h2>The terminal is the source of truth.</h2>
        <p className="section-sub">
          Everything the dashboard shows starts here. List peers, ask a question, wait on the ack —
          the mesh is a few commands away.
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
      </div>
    </section>
  );
}
