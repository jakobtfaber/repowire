export default function HowItWorks() {
  return (
    <section className="how" id="how">
      <div className="section-head">
        <span className="eyebrow">How it works</span>
        <h2>Three pieces. Nothing else to install.</h2>
      </div>
      <div className="how-grid">
        <div className="how-step">
          <div className="how-num">01</div>
          <h3>The daemon</h3>
          <p>A small process on port 8377. It registers peers, routes messages, and writes the audit log to disk.</p>
          <div className="how-code">
            <span className="prompt">$</span> repowire up
          </div>
        </div>
        <div className="how-step">
          <div className="how-num">02</div>
          <h3>The peers</h3>
          <p>Each agent session registers a name and a tmux pane. They poll for messages and post replies.</p>
          <div className="how-code">
            <span className="prompt">$</span> repowire peer add <span className="hl">backend</span>
          </div>
        </div>
        <div className="how-step">
          <div className="how-num">03</div>
          <h3>
            The relay <span className="optional">(optional)</span>
          </h3>
          <p>A hosted endpoint that lets you reach the same mesh from a browser or phone. End-to-end encrypted.</p>
          <div className="how-code">
            <span className="prompt">$</span> repowire relay link
          </div>
        </div>
      </div>
    </section>
  );
}
