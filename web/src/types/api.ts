/** Browser-visible API contract. Credentials and backend paths never belong here. */
export type JobState =
  | "queued"
  | "running"
  | "verifying"
  | "cleaning"
  | "completed"
  | "failed"
  | "cancelled";
export type JobSource = "installed" | "app-store" | "upload";
export interface Job {
  id: string;
  target: string;
  source: JobSource;
  state: JobState;
  phase: string;
  attempts: number;
  cancelRequested: boolean;
  errorMessage?: string;
  cleanupError?: string;
  createdAt: string;
  expiresAt?: string;
  artifactExpired: boolean;
  bytes: number;
  sha256?: string;
  bundleId?: string;
  version?: string;
  installed: boolean;
  replaced: boolean;
  uninstalled: boolean;
  current: number;
  total: number;
}
export interface Status {
  device: { activeJob?: string; cleanupRequired: boolean; reason?: string };
  authJob: string;
  appleEnabled: boolean;
  maxUploadBytes: number;
  queueLimit: number;
  cleanupReviewRequired: boolean;
}
