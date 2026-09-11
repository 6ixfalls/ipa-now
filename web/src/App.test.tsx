import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App";
import { makeJob, makeStatus } from "./testing/fixtures";
import type { Job, Status } from "./types/api";

let state: Status;
let jobs: Job[];
beforeEach(() => {
  jobs = [];
  state = makeStatus();
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init?: RequestInit) => {
      if (url === "/api/status") return Response.json(state);
      if (url === "/api/jobs" && init?.method === "POST") {
        const body = JSON.parse(init.body as string);
        const job = makeJob({
          target: body.target,
          source: body.source,
        });
        jobs.push(job);
        return Response.json(job, { status: 202 });
      }
      if (url === "/api/jobs") return Response.json(jobs);
      return new Response(null, { status: 204 });
    }),
  );
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});
describe("workspace", () => {
  it("renders a real empty state and enqueues an installed app", async () => {
    const user = userEvent.setup();
    render(<App />);
    expect(await screen.findByText("A clean slate.")).toBeTruthy();
    await waitFor(() => expect(screen.getByText("Available")).toBeTruthy());
    await user.type(
      screen.getByLabelText("Bundle identifier"),
      "com.example.app",
    );
    expect(
      (
        screen.getByRole("button", {
          name: /Add to queue/,
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(false);
    await user.click(screen.getByRole("button", { name: /Add to queue/ }));
    expect(await screen.findByRole("status")).toBeTruthy();
    expect(screen.getByRole("region", { name: "Job details" })).toBeTruthy();
    expect(fetch).toHaveBeenCalledWith(
      "/api/jobs",
      expect.objectContaining({
        method: "POST",
        body: JSON.stringify({
          target: "com.example.app",
          source: "installed",
          allowReplacement: false,
        }),
      }),
    );
  });
  it("requires replacement acknowledgement for App Store requests", async () => {
    const user = userEvent.setup();
    render(<App />);
    await screen.findByText("Available");
    await user.click(screen.getByRole("button", { name: "App Store" }));
    await user.type(screen.getByLabelText("App identifier or URL"), "123456");
    expect(
      (
        screen.getByRole("button", {
          name: /Add to queue/,
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(true);
    await user.click(screen.getByLabelText(/Allow this build to replace/));
    expect(
      (
        screen.getByRole("button", {
          name: /Add to queue/,
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(false);
  });
  it("shows connection failures rather than invented jobs", async () => {
    vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new Error("offline")));
    render(<App />);
    expect((await screen.findByRole("alert")).textContent).toContain(
      "Cannot reach ipa-now",
    );
    expect(
      (
        screen.getByRole("button", {
          name: /Add to queue/,
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(true);
  });
});

describe("job-specific forms", () => {
  it("does not carry cleanup confirmation to a different job", async () => {
    const first = makeJob({
      state: "cleaning",
      cleanupError: "Device review required",
    });
    const second = makeJob({
      id: "abcdef1234567890abcdef1234567890",
      target: "com.example.other",
      state: "cleaning",
      cleanupError: "Device review required",
    });
    jobs = [first, second];
    state = makeStatus({ device: { cleanupRequired: true } });
    const user = userEvent.setup();
    render(<App />);
    await user.click(
      await screen.findByRole("button", { name: /com.example.app/ }),
    );
    const confirmation = screen.getByLabelText(
      "I inspected the device and completed all required cleanup.",
    );
    await user.click(confirmation);
    expect(
      (
        screen.getByRole("button", {
          name: "Confirm cleanup & release device",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(false);
    await user.click(screen.getByRole("button", { name: /com.example.other/ }));
    expect(
      (
        screen.getByLabelText(
          "I inspected the device and completed all required cleanup.",
        ) as HTMLInputElement
      ).checked,
    ).toBe(false);
    expect(
      (
        screen.getByRole("button", {
          name: "Confirm cleanup & release device",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(true);
  });
  it("submits 2FA through the active job endpoint and clears the code", async () => {
    const job = makeJob({ state: "running" });
    jobs = [job];
    state = makeStatus({ authJob: job.id });
    const user = userEvent.setup();
    render(<App />);
    await user.click(await screen.findByRole("button", { name: /Enter code/ }));
    await user.type(screen.getByLabelText("Apple verification code"), "123456");
    await user.click(screen.getByRole("button", { name: "Submit code" }));
    await waitFor(() =>
      expect(fetch).toHaveBeenCalledWith(
        `/api/jobs/${job.id}/auth-code`,
        expect.objectContaining({
          method: "POST",
          body: JSON.stringify({ code: "123456" }),
        }),
      ),
    );
    expect(
      (screen.getByLabelText("Apple verification code") as HTMLInputElement)
        .value,
    ).toBe("");
  });
});
