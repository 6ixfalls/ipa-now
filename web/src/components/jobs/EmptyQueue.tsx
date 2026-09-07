import type { JobFilter } from "../../utils/jobs";
export function EmptyQueue({ filter }: { filter: JobFilter }) {
  return (
    <div className="empty">
      <div className="empty-art">
        ↓<span>✓</span>
      </div>
      <h3>{filter === "all" ? "A clean slate." : "No matching jobs."}</h3>
      <p>
        {filter === "all"
          ? "Your requests will appear here, from the first connection to the finished IPA."
          : "Try another filter or add a new request."}
      </p>
      <span className="empty-caption">
        PRIVATE BY DEFAULT. READY WHEN YOU ARE.
      </span>
    </div>
  );
}
