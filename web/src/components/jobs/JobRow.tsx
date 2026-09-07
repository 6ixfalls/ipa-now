import type { Job } from "../../types/api";
import { formatLabel } from "../../utils/format";
import { isTerminal } from "../../utils/jobs";
export function JobRow({
  job: j,
  selectedId,
  onSelect,
}: {
  job: Job;
  selectedId?: string;
  onSelect: (id: string) => void;
}) {
  return (
    <button
      type="button"
      className={`job-row ${j.id === selectedId ? "chosen" : ""}`}
      onClick={() => onSelect(j.id)}
    >
      <span className={`app-icon ${j.state}`}>
        {j.state === "completed" ? "↓" : j.cleanupError ? "!" : "▧"}
      </span>
      <span className="job-info">
        <strong>{j.target}</strong>
        <small>
          {formatLabel(j.source)} ·{" "}
          {new Date(j.createdAt).toLocaleTimeString([], {
            hour: "2-digit",
            minute: "2-digit",
          })}
        </small>
      </span>
      <span className={`state ${j.state}`}>
        {j.cleanupError
          ? "Review cleanup"
          : j.cancelRequested && !isTerminal(j.state)
            ? "Cancelling"
            : formatLabel(j.state)}
      </span>
      <span className="row-arrow">›</span>
    </button>
  );
}
