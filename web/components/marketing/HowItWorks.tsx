import Image from "next/image";

export default function HowItWorks() {
  return (
    <section className="how" id="how">
      <div className="section-head">
        <span className="eyebrow">How it works</span>
        <h2>One daemon, many peers, optional relay.</h2>
        <p className="section-sub">
          The same architecture from the README: agents and human surfaces connect to the local
          daemon, and the hosted relay only appears when you opt into remote access.
        </p>
      </div>
      <div className="arch-frame">
        <Image
          src="/brand/repowire-arch.webp"
          width={1400}
          height={886}
          alt="Repowire architecture diagram showing agent transports, the local daemon, and optional relay surfaces"
          sizes="(max-width: 980px) 100vw, 1100px"
          className="arch-img"
        />
      </div>
    </section>
  );
}
