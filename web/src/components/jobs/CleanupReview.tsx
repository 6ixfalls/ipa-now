import { useState } from "react";
import { confirmCleanup } from "../../api/jobs";
import type { RunAction } from "../../hooks/useAction";
import type { Job } from "../../types/api";
export function CleanupReview({
  job: detail,
  busy,
  action,
}: {
  job: Job;
  busy: boolean;
  action: RunAction;
}) {
  const [confirmed, setConfirmed] = useState(false);
  return (
    <div className="cleanup">
      <h3>Finish device cleanup</h3>
      <p>
        The pinned decryption engine cannot report or recover every remote
        resource. Inspect the dedicated device before releasing it:
      </p>
      <ul>
        <li>
          Inspect <code>/var/mobile/Media/ipadecrypt</code> and this job’s{" "}
          <code>/tmp/ipadecrypt-&lt;pid&gt;</code> directory for staging and
          helper files. Remove only resources owned by this job.
        </li>
        <li>
          Check for an app installed or replaced by the job. Apply the automatic
          uninstall policy; preserve apps that existed before an installed-app
          request.
        </li>
        <li>
          Verify no helper is running and no job-owned temporary files remain. A
          previous app build is never assumed restored.
        </li>
      </ul>
      <p>
        Reported result: installed {detail.installed ? "yes" : "not reported"},
        replaced {detail.replaced ? "yes" : "not reported"}, uninstalled{" "}
        {detail.uninstalled ? "yes" : "not reported"}. On failure, these flags
        may be unavailable.
      </p>
      <label className="check">
        <input
          type="checkbox"
          checked={confirmed}
          onChange={(e) => setConfirmed(e.target.checked)}
        />
        <span>I inspected the device and completed all required cleanup.</span>
      </label>
      <button
        type="button"
        className="secondary"
        disabled={!confirmed || busy}
        onClick={() =>
          void action(
            () => confirmCleanup(detail.id),
            "Cleanup confirmed. The queue can continue.",
          )
        }
      >
        Confirm cleanup & release device
      </button>
    </div>
  );
}
