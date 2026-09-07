import { artifactUrl, cancelJob } from "../../api/jobs";
import type { RunAction } from "../../hooks/useAction";
import type { Job, Status } from "../../types/api";
import { formatBytes, formatLabel } from "../../utils/format";
import { hasDownload, isTerminal } from "../../utils/jobs";
import { AuthCodeForm } from "./AuthCodeForm";
import { CleanupReview } from "./CleanupReview";
import { JobProgress } from "./JobProgress";
export function JobDetails({
  job: detail,
  status,
  busy,
  action,
  onClose,
}: {
  job: Job;
  status?: Status;
  busy: boolean;
  action: RunAction;
  onClose: () => void;
}) {
  return (
    <section className="detail" aria-label="Job details">
      <div className="detail-heading">
        <div>
          <span className="step-label">
            JOB DETAILS / {detail.id.slice(0, 8)}
          </span>
          <h2>{detail.target}</h2>
        </div>
        <button
          type="button"
          className="text-button"
          aria-label="Close job details"
          onClick={onClose}
        >
          ×
        </button>
      </div>
      <JobProgress state={detail.state} />
      <div className="detail-meta">
        <span>
          Phase <strong>{formatLabel(detail.phase || detail.state)}</strong>
        </span>
        <span>
          Attempt <strong>{detail.attempts}</strong>
        </span>
        <span>
          Output <strong>{formatBytes(detail.bytes)}</strong>
        </span>
        {detail.expiresAt && (
          <span>
            Retention ends{" "}
            <strong>{new Date(detail.expiresAt).toLocaleString()}</strong>
          </span>
        )}
      </div>
      {detail.total > 0 && !isTerminal(detail.state) && (
        <progress
          aria-label="Transfer progress"
          value={detail.current}
          max={Math.max(detail.total, detail.current)}
        />
      )}
      {detail.errorMessage && (
        <p className="inline-warning">{detail.errorMessage}</p>
      )}
      {status?.authJob === detail.id && (
        <AuthCodeForm jobId={detail.id} busy={busy} action={action} />
      )}
      {detail.cleanupError && (
        <CleanupReview job={detail} busy={busy} action={action} />
      )}
      <div className="detail-actions">
        {!isTerminal(detail.state) && detail.state !== "cleaning" && (
          <button
            type="button"
            className="secondary"
            disabled={busy || detail.cancelRequested}
            onClick={() =>
              void action(
                () => cancelJob(detail.id),
                "Cancellation requested. Cleanup will run before the job finishes.",
              )
            }
          >
            {detail.cancelRequested ? "Cancellation requested" : "Cancel job"}
          </button>
        )}
        {hasDownload(detail) && (
          <a className="primary download" href={artifactUrl(detail.id)}>
            Download IPA <span>↓</span>
          </a>
        )}
        {detail.artifactExpired && (
          <span className="muted">
            Artifact expired under the retention policy.
          </span>
        )}
      </div>
      {detail.sha256 && (
        <p className="checksum">
          SHA-256 <code>{detail.sha256}</code>
        </p>
      )}
    </section>
  );
}
