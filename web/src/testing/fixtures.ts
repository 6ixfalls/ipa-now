import type { Job, Status } from "../types/api";
export function makeJob(overrides: Partial<Job> = {}): Job {
  return {
    id: "1234567890abcdef1234567890abcdef",
    target: "com.example.app",
    source: "installed",
    state: "queued",
    phase: "",
    attempts: 0,
    cancelRequested: false,
    artifactExpired: false,
    createdAt: "2026-09-06T00:00:00Z",
    bytes: 0,
    current: 0,
    total: 0,
    installed: false,
    replaced: false,
    uninstalled: false,
    ...overrides,
  };
}
export function makeStatus(overrides: Partial<Status> = {}): Status {
  return {
    device: { cleanupRequired: false },
    authJob: "",
    appleEnabled: true,
    maxUploadBytes: 2147483648,
    queueLimit: 20,
    cleanupReviewRequired: true,
    ...overrides,
  };
}
