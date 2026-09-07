import type { JobState } from "../../types/api";
import { JOB_STAGES } from "../../utils/jobs";
export function JobProgress({ state }: { state: JobState }) {
  return (
    <ol className="pipeline">
      {JOB_STAGES.map((stage, i) => (
        <li
          key={stage}
          className={JOB_STAGES.indexOf(state) >= i ? "reached" : ""}
        >
          <span>{i + 1}</span>
          {stage}
        </li>
      ))}
    </ol>
  );
}
