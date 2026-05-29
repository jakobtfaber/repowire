import { afterEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, render } from "@testing-library/react";
import MeshDemo from "./MeshDemo";

describe("MeshDemo", () => {
  afterEach(() => {
    cleanup();
    vi.useRealTimers();
  });

  it("rotates the highlighted log row on the 2.2s interval", () => {
    vi.useFakeTimers();
    const { container } = render(<MeshDemo />);

    const firstHighlight = container.querySelector(".mesh-log-row.row-hl");
    expect(firstHighlight?.textContent).toContain("14:02:11");

    act(() => {
      vi.advanceTimersByTime(2200);
    });

    const nextHighlight = container.querySelector(".mesh-log-row.row-hl");
    expect(nextHighlight?.textContent).toContain("14:02:18");
  });

  it("clears its interval on unmount", () => {
    vi.useFakeTimers();
    const clearSpy = vi.spyOn(globalThis, "clearInterval");
    const { unmount } = render(<MeshDemo />);
    unmount();
    expect(clearSpy).toHaveBeenCalled();
  });
});
