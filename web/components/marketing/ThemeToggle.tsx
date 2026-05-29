"use client";

import { useSyncExternalStore } from "react";
import { Moon, Sun } from "lucide-react";

const STORAGE_KEY = "repowire-theme";

type Theme = "light" | "dark";

// The data-theme attribute is the source of truth: the no-flash inline script in
// layout.tsx sets it before hydration. We read it via useSyncExternalStore so the
// icon stays in sync without a setState-in-effect cascade or hydration mismatch.
function subscribe(onChange: () => void) {
  const observer = new MutationObserver(onChange);
  observer.observe(document.documentElement, { attributes: true, attributeFilter: ["data-theme"] });
  return () => observer.disconnect();
}

function getSnapshot(): Theme {
  return document.documentElement.getAttribute("data-theme") === "dark" ? "dark" : "light";
}

export default function ThemeToggle() {
  // Server snapshot is "light" (the light-first SSR default).
  const theme = useSyncExternalStore(subscribe, getSnapshot, () => "light" as Theme);

  function toggle() {
    const next: Theme = theme === "dark" ? "light" : "dark";
    document.documentElement.setAttribute("data-theme", next);
    try {
      localStorage.setItem(STORAGE_KEY, next);
    } catch {
      // localStorage may be unavailable (private mode); theme still applies for the session.
    }
  }

  return (
    <button
      className="icon-btn"
      onClick={toggle}
      aria-label={theme === "dark" ? "Switch to light mode" : "Switch to dark mode"}
    >
      {theme === "dark" ? (
        <Sun width={16} height={16} strokeWidth={1.75} />
      ) : (
        <Moon width={16} height={16} strokeWidth={1.75} />
      )}
    </button>
  );
}
