import { afterEach, expect, it, vi } from "vitest";
import { makeJob } from "../testing/fixtures";
import { createJob } from "./jobs";

afterEach(() => vi.unstubAllGlobals());
it("sends raw IPA bytes and replacement acknowledgement through the shared client", async () => {
  const file = new File(["synthetic fixture"], "example.ipa");
  const job = makeJob({ source: "upload" });
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue(Response.json(job, { status: 202 })),
  );
  expect(
    await createJob(
      {
        source: "upload",
        target: "",
        file,
        allowReplacement: true,
      },
      1024,
    ),
  ).toEqual(job);
  expect(fetch).toHaveBeenCalledWith(
    "/api/uploads",
    expect.objectContaining({
      method: "POST",
      body: file,
      headers: {
        "Content-Type": "application/octet-stream",
        "X-IPA-Now": "1",
        "X-IPA-Allow-Replacement": "true",
      },
    }),
  );
});
it("rejects oversized uploads before sending any bytes", async () => {
  const fetchMock = vi.fn();
  vi.stubGlobal("fetch", fetchMock);
  await expect(
    createJob(
      {
        source: "upload",
        target: "",
        file: new File(["too large"], "example.ipa"),
        allowReplacement: true,
      },
      1,
    ),
  ).rejects.toThrow("The file exceeds the upload limit.");
  expect(fetchMock).not.toHaveBeenCalled();
});
it("preserves safe API error messages and handles non-JSON failures", async () => {
  vi.stubGlobal(
    "fetch",
    vi
      .fn()
      .mockResolvedValueOnce(
        Response.json({ message: "The queue is full." }, { status: 429 }),
      )
      .mockResolvedValueOnce(new Response("unavailable", { status: 503 })),
  );
  const request = {
    source: "installed" as const,
    target: "com.example.app",
    allowReplacement: false,
  };
  await expect(createJob(request, 1024)).rejects.toThrow("The queue is full.");
  await expect(createJob(request, 1024)).rejects.toThrow(
    "The service could not complete the request.",
  );
});
