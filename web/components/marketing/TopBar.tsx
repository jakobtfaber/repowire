"use client";

import Image from "next/image";
import { Menu } from "lucide-react";
import ThemeToggle from "./ThemeToggle";
import GitHubMark from "./GitHubMark";

const GITHUB_URL = "https://github.com/prassanna-ravishankar/repowire";

export default function TopBar() {
  return (
    <header className="topbar">
      <div className="topbar-inner">
        <a className="brand" href="#">
          <Image src="/brand/logo-mark.svg" width={20} height={22} alt="" priority style={{ height: 22, width: "auto" }} />
          <span>Repowire</span>
        </a>
        <nav className="topnav">
          <a href="#features">Product</a>
          <a href="https://docs.repowire.io">Docs</a>
          <a href="https://github.com/prassanna-ravishankar/repowire/releases">Changelog</a>
        </nav>
        <div className="top-actions">
          <ThemeToggle />
          <a className="icon-btn" href={GITHUB_URL} target="_blank" rel="noreferrer" aria-label="GitHub">
            <GitHubMark size={16} />
          </a>
          <a className="cta" href="#install">Get started</a>
          <button className="icon-btn mobile-toggle" aria-label="Menu">
            <Menu width={18} height={18} strokeWidth={1.75} />
          </button>
        </div>
      </div>
    </header>
  );
}
