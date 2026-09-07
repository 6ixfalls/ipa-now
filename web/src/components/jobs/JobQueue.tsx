import type { Job, Status } from "../../types/api";
import { filterJobs, JOB_FILTERS, type JobFilter } from "../../utils/jobs";
import { EmptyQueue } from "./EmptyQueue";
import { JobRow } from "./JobRow";
export function JobQueue({
  jobs,
  status,
  filter,
  selected,
  onFilterChange,
  onSelect,
}: {
  jobs: Job[];
  status?: Status;
  filter: JobFilter;
  selected?: string;
  onFilterChange: (filter: JobFilter) => void;
  onSelect: (id: string) => void;
}) {
  const visible = filterJobs(jobs, filter);
  return (
    <section className="queue-card">
      <div className="queue-heading">
        <div>
          <span className="step-label">02 / JOB ACTIVITY</span>
          <h2>
            Your queue <span className="queue-count">{jobs.length}</span>
          </h2>
        </div>
        <span className="live">
          <i />
          {status ? "Live" : "Connecting"}
        </span>
      </div>
      <fieldset className="filters" aria-label="Filter jobs">
        {JOB_FILTERS.map(([value, label]) => (
          <button
            type="button"
            key={value}
            className={filter === value ? "active" : ""}
            onClick={() => onFilterChange(value)}
          >
            {label}
          </button>
        ))}
      </fieldset>
      {visible.length === 0 ? (
        <EmptyQueue filter={filter} />
      ) : (
        <div className="job-list">
          {visible.map((j) => (
            <JobRow
              key={j.id}
              job={j}
              selectedId={selected}
              onSelect={onSelect}
            />
          ))}
        </div>
      )}
    </section>
  );
}
