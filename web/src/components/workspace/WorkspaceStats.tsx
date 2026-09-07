import type { Status } from "../../types/api";
export function WorkspaceStats({
  activeCount,
  completedCount,
  status,
}: {
  activeCount: number;
  completedCount: number;
  status?: Status;
}) {
  return (
    <section className="stats" aria-label="Workspace overview">
      <div>
        <span className="stat-label">In the queue</span>
        <strong>
          {activeCount.toString().padStart(2, "0")}
          <small className="stat-unit">jobs</small>
        </strong>
      </div>
      <div>
        <span className="stat-label">Ready to download</span>
        <strong>
          {completedCount.toString().padStart(2, "0")}
          <small className="stat-unit">artifacts</small>
        </strong>
      </div>
      <div>
        <span className="stat-label">Device allocation</span>
        <strong className="device-stat">
          <i
            className={
              status?.device.cleanupRequired ? "amber-dot" : "green-dot"
            }
          />
          {!status
            ? "Loading"
            : status.device.cleanupRequired
              ? "Needs review"
              : status.device.activeJob
                ? "In use"
                : "Available"}
        </strong>
        <small className="stat-caption">Single device · exclusive access</small>
      </div>
    </section>
  );
}
