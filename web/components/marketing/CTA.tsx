import CopyButton from "./CopyButton";

const INSTALL_CMD = "uv tool install repowire && repowire up";

export default function CTA() {
  return (
    <section className="cta-band" id="install">
      <h2>Wire up your agents in one command.</h2>
      <p>Free, local, open source. The hosted relay starts at $0 for solo developers.</p>
      <div className="install-strip large">
        <div className="install-cmd">
          <span className="install-prompt">$</span>
          <span className="install-text">{INSTALL_CMD}</span>
        </div>
        <CopyButton text={INSTALL_CMD} />
      </div>
      <div className="cta-meta">Requires macOS or Linux · Python 3.10+ · tmux 3.0+</div>
    </section>
  );
}
