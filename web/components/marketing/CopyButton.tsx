"use client";

import { useState } from "react";
import { Copy } from "lucide-react";

export default function CopyButton({ text }: { text: string }) {
  const [copied, setCopied] = useState(false);

  function copy() {
    if (!navigator.clipboard) return;
    navigator.clipboard
      .writeText(text)
      .then(() => {
        setCopied(true);
        setTimeout(() => setCopied(false), 1400);
      })
      .catch(() => {
        // Clipboard write was blocked/failed; don't show a false "Copied".
      });
  }

  return (
    <button className="copy-btn" onClick={copy} aria-label="Copy install command">
      <Copy width={14} height={14} strokeWidth={1.75} />
      <span className="copy-label">{copied ? "Copied" : "Copy"}</span>
    </button>
  );
}
