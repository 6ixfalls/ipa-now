import type { Job, JobSource, JobState } from "../types/api";
export const DEFAULT_UPLOAD_LIMIT = 2147483648;
export const JOB_SOURCES: ReadonlyArray<readonly [JobSource, string]> = [
  ["installed", "Installed app"],
  ["app-store", "App Store"],
  ["upload", "Upload IPA"],
];
export type JobFilter = "all" | "active" | "completed" | "failed";
export const JOB_FILTERS: ReadonlyArray<readonly [JobFilter, string]> = [
  ["all", "All jobs"],
  ["active", "Active"],
  ["completed", "Completed"],
  ["failed", "Failed"],
];
export const JOB_STAGES: readonly JobState[] = [
  "queued",
  "running",
  "verifying",
  "cleaning",
  "completed",
];
export function isTerminal(state: JobState): boolean {
  return state === "completed" || state === "failed" || state === "cancelled";
}
export function filterJobs(jobs: Job[], filter: JobFilter): Job[] {
  return jobs.filter(
    (job) =>
      filter === "all" ||
      (filter === "active" ? !isTerminal(job.state) : job.state === filter),
  );
}
export function hasDownload(job: Job, now = Date.now()): boolean {
  return (
    job.state === "completed" &&
    !job.artifactExpired &&
    Boolean(job.expiresAt && Date.parse(job.expiresAt) > now)
  );
}
