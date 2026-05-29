import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import ThemeToggle from "./ThemeToggle";

// The MutationObserver in useSyncExternalStore notifies React asynchronously, so
// nudge microtasks inside act() to flush the resulting re-render after each click.
async function flush() {
  await act(async () => {
    await Promise.resolve();
  });
}

describe("ThemeToggle", () => {
  beforeEach(() => {
    document.documentElement.setAttribute("data-theme", "light");
    localStorage.clear();
  });
  afterEach(cleanup);

  it("reflects the current data-theme (light by default)", () => {
    render(<ThemeToggle />);
    expect(screen.getByRole("button")).toHaveAttribute("aria-label", "Switch to dark mode");
  });

  it("toggles data-theme and persists the choice to localStorage", async () => {
    render(<ThemeToggle />);
    const btn = screen.getByRole("button");

    fireEvent.click(btn);
    await flush();
    expect(document.documentElement.getAttribute("data-theme")).toBe("dark");
    expect(localStorage.getItem("repowire-theme")).toBe("dark");
    expect(btn).toHaveAttribute("aria-label", "Switch to light mode");

    fireEvent.click(btn);
    await flush();
    expect(document.documentElement.getAttribute("data-theme")).toBe("light");
    expect(localStorage.getItem("repowire-theme")).toBe("light");
  });
});
