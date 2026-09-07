import { useState } from "react";
import { JobDetails } from "./components/jobs/JobDetails";
import { JobQueue } from "./components/jobs/JobQueue";
import { WorkspaceLayout } from "./components/layout/WorkspaceLayout";
import { RequestForm } from "./components/requests/RequestForm";
import { Alert } from "./components/ui/Alert";
import { WorkspaceAlerts } from "./components/workspace/WorkspaceAlerts";
import { WorkspaceStats } from "./components/workspace/WorkspaceStats";
import { useAction } from "./hooks/useAction";
import { useWorkspace } from "./hooks/useWorkspace";
import { isTerminal, type JobFilter } from "./utils/jobs";

export default function App() {
  const workspace = useWorkspace();
  const { jobs, status } = workspace;
  const feedback = useAction(workspace.refresh);
  const [filter, setFilter] = useState<JobFilter>("all");
  const [selected, setSelected] = useState<string>();
  const detail = jobs.find((job) => job.id === selected);
  const activeCount = jobs.filter((job) => !isTerminal(job.state)).length;
  const completedCount = jobs.filter(
    (job) => job.state === "completed" && !job.artifactExpired,
  ).length;
  const error = feedback.error || workspace.error;
  return (
    <WorkspaceLayout
      activeCount={activeCount}
      completedCount={completedCount}
      onQueue={() => {
        setFilter("all");
        setSelected(undefined);
      }}
      onArtifacts={() => setFilter("completed")}
    >
      {error && (
        <Alert
          variant="error"
          dismissLabel="Dismiss error"
          onDismiss={() => {
            feedback.dismissError();
            workspace.dismissError();
          }}
        >
          {error}
        </Alert>
      )}
      {feedback.notice && (
        <Alert
          variant="notice"
          dismissLabel="Dismiss notification"
          onDismiss={feedback.dismissNotice}
        >
          {feedback.notice}
        </Alert>
      )}
      <WorkspaceStats
        activeCount={activeCount}
        completedCount={completedCount}
        status={status}
      />
      <WorkspaceAlerts jobs={jobs} status={status} onSelect={setSelected} />
      <div className="work-grid">
        <RequestForm
          status={status}
          busy={feedback.busy}
          action={feedback.action}
          onCreated={(job) => setSelected(job.id)}
        />
        <JobQueue
          jobs={jobs}
          status={status}
          filter={filter}
          selected={selected}
          onFilterChange={setFilter}
          onSelect={setSelected}
        />
      </div>
      {detail && (
        <JobDetails
          key={detail.id}
          job={detail}
          status={status}
          busy={feedback.busy}
          action={feedback.action}
          onClose={() => setSelected(undefined)}
        />
      )}
    </WorkspaceLayout>
  );
}
