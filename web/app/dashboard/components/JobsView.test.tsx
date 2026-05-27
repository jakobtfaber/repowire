import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { JobsView } from "./JobsView";
import type { JobsResponse } from "../types";

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

const QUEUED_JOB = {
  job_id: "work-1",
  title: "Audit docs",
  kind: "review",
  state: "queued",
  circle: "default",
  created_at: "2026-05-01T10:00:00Z",
  updated_at: "2026-05-01T10:05:00Z",
  due_at: "2026-05-01T11:00:00Z",
  execution: {
    prompt: { body: "Review the docs IA." },
    target: { path: "/repo/repowire", backend: "codex", profile: "fast" },
    delivery: { kind: "ask" },
  },
};

const RECURRING_JOB = {
  calendar_id: "cal-1",
  title: "Nightly maintenance",
  kind: "maintenance",
  state: "active",
  cron: "@daily",
  circle: "default",
  created_at: "2026-05-01T09:00:00Z",
  updated_at: "2026-05-01T09:00:00Z",
  next_due_at: "2026-05-02T09:00:00Z",
  execution: {
    prompt: { body: "Run the maintenance pass." },
    target: { path: "/repo/repowire", backend: "claude-code" },
    delivery: { kind: "notify" },
  },
};

const FAILED_JOB = {
  ...QUEUED_JOB,
  job_id: "work-failed",
  title: "Fix CI",
  state: "failed",
  result_summary: "pytest failed",
  progress_events: [{ at: "2026-05-01T10:10:00Z", note: "runner failed" }],
};

describe("JobsView", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("renders daemon jobs and recurring templates", async () => {
    const payload: JobsResponse = {
      work: [QUEUED_JOB],
      recurring: [RECURRING_JOB],
    };
    const fetchMock = vi.fn(async () => jsonResponse(payload));
    vi.stubGlobal("fetch", fetchMock);

    render(<JobsView apiBase="http://daemon.test" refreshSignal={0} />);

    expect((await screen.findAllByText("Audit docs")).length).toBeGreaterThan(0);
    expect(screen.getByText("Nightly maintenance")).toBeInTheDocument();
    expect(screen.getByText("Review the docs IA.")).toBeInTheDocument();
    expect(screen.getAllByText("active").length).toBeGreaterThan(0);
    expect(fetchMock).toHaveBeenCalledWith("http://daemon.test/jobs");
  });

  it("cancels a recurring template through the jobs control route", async () => {
    const active: JobsResponse = { work: [], recurring: [RECURRING_JOB] };
    const cancelled: JobsResponse = {
      work: [],
      recurring: [{ ...RECURRING_JOB, state: "cancelled" }],
    };
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(jsonResponse(active))
      .mockResolvedValueOnce(jsonResponse({ ok: true }))
      .mockResolvedValueOnce(jsonResponse(cancelled));
    vi.stubGlobal("fetch", fetchMock);

    render(<JobsView apiBase="http://daemon.test" refreshSignal={0} />);

    fireEvent.click(await screen.findByRole("button", { name: /cancel/i }));

    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledWith(
        "http://daemon.test/jobs/cal-1/cancel",
        expect.objectContaining({
          method: "POST",
          body: JSON.stringify({ reason: "dashboard" }),
        }),
      );
    });
    await waitFor(() => expect(screen.getAllByText("cancelled").length).toBeGreaterThan(0));
  });

  it("offers retry and run controls for failed work jobs", async () => {
    const failed: JobsResponse = { work: [FAILED_JOB], recurring: [] };
    const retried: JobsResponse = {
      work: [{ ...FAILED_JOB, state: "queued", result_summary: null }],
      recurring: [],
    };
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(jsonResponse(failed))
      .mockResolvedValueOnce(jsonResponse({ ok: true }))
      .mockResolvedValueOnce(jsonResponse(retried));
    vi.stubGlobal("fetch", fetchMock);

    render(<JobsView apiBase="http://daemon.test" refreshSignal={0} />);

    expect(await screen.findByRole("button", { name: /retry/i })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /run now/i })).toBeInTheDocument();
    expect(screen.getByText("runner failed")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: /retry/i }));

    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledWith(
        "http://daemon.test/jobs/work-failed/retry",
        expect.objectContaining({ method: "POST" }),
      );
    });
    await waitFor(() => expect(screen.getAllByText("queued").length).toBeGreaterThan(0));
  });
});
