import type { Job, JobSource, Status } from "../types/api";
import { apiUrl, get, post, readResponse } from "./client";
export interface JobSubmission {
  source: JobSource;
  target: string;
  file?: File;
  allowReplacement: boolean;
}
export async function loadWorkspace(signal?: AbortSignal) {
  const [jobs, status] = await Promise.all([
    get<Job[]>("/jobs", signal),
    get<Status>("/status", signal),
  ]);
  return { jobs, status };
}
export async function createJob(
  request: JobSubmission,
  maxUploadBytes: number,
): Promise<Job> {
  const { source, target, file, allowReplacement } = request;
  if (source !== "upload")
    return post<Job>("/jobs", {
      target: target.trim(),
      source,
      allowReplacement,
    });
  if (!file) throw new Error("Choose an IPA file.");
  if (file.size > maxUploadBytes)
    throw new Error("The file exceeds the upload limit.");
  return readResponse<Job>(
    await fetch(apiUrl("/uploads"), {
      method: "POST",
      headers: {
        "Content-Type": "application/octet-stream",
        "X-IPA-Now": "1",
        "X-IPA-Allow-Replacement": String(allowReplacement),
      },
      body: file,
    }),
  );
}
export function cancelJob(id: string) {
  return post<void>(`/jobs/${encodeURIComponent(id)}/cancel`, {});
}
export function submitAuthCode(id: string, code: string) {
  return post<void>(`/jobs/${encodeURIComponent(id)}/auth-code`, { code });
}
export function confirmCleanup(id: string) {
  return post<void>(`/jobs/${encodeURIComponent(id)}/confirm-cleanup`, {
    confirmed: true,
  });
}
export function artifactUrl(id: string) {
  return apiUrl(`/jobs/${encodeURIComponent(id)}/artifact`);
}
