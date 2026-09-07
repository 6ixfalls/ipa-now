import type { Job, Status } from "../../types/api";
import { Alert } from "../ui/Alert";
export function WorkspaceAlerts({
  jobs,
  status,
  onSelect,
}: {
  jobs: Job[];
  status?: Status;
  onSelect: (id: string) => void;
}) {
  const cleanupJob = jobs.find((job) => job.cleanupError);
  return (
    <>
      {status?.authJob && (
        <Alert variant="warning">
          <div>
            <strong>Apple verification code needed</strong>
            <p>
              The active job is waiting for a code from your trusted Apple
              device.
            </p>
          </div>
          <button type="button" onClick={() => onSelect(status.authJob)}>
            Enter code →
          </button>
        </Alert>
      )}
      {status?.device.cleanupRequired && (
        <Alert variant="warning">
          <div>
            <strong>Device cleanup needs your attention</strong>
            <p>
              The queue is paused. Open the job awaiting cleanup and follow the
              review steps.
            </p>
          </div>
          <button
            type="button"
            disabled={!cleanupJob}
            onClick={() => {
              if (cleanupJob) onSelect(cleanupJob.id);
            }}
          >
            Review cleanup →
          </button>
        </Alert>
      )}
    </>
  );
}
