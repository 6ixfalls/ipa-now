import { type FormEvent, useRef, useState } from "react";
import { createJob } from "../../api/jobs";
import type { RunAction } from "../../hooks/useAction";
import type { Job, JobSource, Status } from "../../types/api";
import { formatBytes } from "../../utils/format";
import { DEFAULT_UPLOAD_LIMIT, JOB_SOURCES } from "../../utils/jobs";
export function RequestForm({
  status,
  busy,
  action,
  onCreated,
}: {
  status?: Status;
  busy: boolean;
  action: RunAction;
  onCreated: (job: Job) => void;
}) {
  const [source, setSource] = useState<JobSource>("installed");
  const [target, setTarget] = useState("");
  const [file, setFile] = useState<File>();
  const [replace, setReplace] = useState(false);

  const input = useRef<HTMLInputElement>(null);
  const submit = (event: FormEvent) => {
    event.preventDefault();
    void action(async () => {
      const job = await createJob(
        { source, target, file, allowReplacement: replace },
        status?.maxUploadBytes ?? DEFAULT_UPLOAD_LIMIT,
      );
      onCreated(job);
      setTarget("");
      setFile(undefined);
      setReplace(false);
      if (input.current) input.current.value = "";
    }, "Request added to the queue.");
  };
  return (
    <section className="request-card">
      <div className="card-heading">
        <span className="step-label">01 / NEW REQUEST</span>
        <span className="plus-icon">＋</span>
      </div>
      <h2>What are we decrypting?</h2>
      <p className="muted">
        Use an installed app, your App Store account, or an IPA file.
      </p>
      <fieldset className="source-tabs" aria-label="App source">
        {JOB_SOURCES.map(([value, label]) => (
          <button
            type="button"
            key={value}
            aria-pressed={source === value}
            className={source === value ? "active" : ""}
            onClick={() => {
              setSource(value);
              setReplace(false);
            }}
          >
            {label}
          </button>
        ))}
      </fieldset>
      <form onSubmit={submit}>
        {source !== "upload" ? (
          <>
            <label htmlFor="target">
              {source === "installed"
                ? "Bundle identifier"
                : "App identifier or URL"}
            </label>
            <input
              id="target"
              value={target}
              onChange={(e) => setTarget(e.target.value)}
              placeholder={
                source === "installed"
                  ? "com.example.app"
                  : "App Store URL, app ID, or bundle ID"
              }
              required
              maxLength={512}
              autoComplete="off"
              spellCheck={false}
            />
            <p className="field-help">
              {source === "installed" ? (
                <>
                  Uses the existing installation. The app stays on your device.{" "}
                  <a
                    href="https://iosbundleidfinder.vercel.app/"
                    target="_blank"
                    rel="noreferrer"
                  >
                    Find an app&apos;s bundle ID ↗
                  </a>
                </>
              ) : (
                "Downloads through your configured Apple account."
              )}
            </p>
          </>
        ) : (
          <>
            <label htmlFor="ipa-file">Encrypted IPA</label>
            <div className="upload-box">
              <span>↥</span>
              <strong>{file?.name || "Choose an IPA file"}</strong>
              <small>
                {file
                  ? formatBytes(file.size)
                  : `Up to ${formatBytes(status?.maxUploadBytes || DEFAULT_UPLOAD_LIMIT)}`}
              </small>
              <input
                ref={input}
                id="ipa-file"
                type="file"
                accept=".ipa"
                required
                onChange={(e) => setFile(e.target.files?.[0])}
              />
            </div>
          </>
        )}
        {source === "app-store" && !status?.appleEnabled && (
          <p className="inline-warning">
            An operator must configure an Apple account before App Store
            requests are available.
          </p>
        )}
        {source !== "installed" && (
          <label className="check">
            <input
              type="checkbox"
              checked={replace}
              onChange={(e) => setReplace(e.target.checked)}
              required
            />
            <span>
              Allow this build to replace an installed app and be automatically
              uninstalled afterward. The previous build is not restored.
            </span>
          </label>
        )}
        <button
          type="submit"
          className="primary"
          disabled={
            busy ||
            !status ||
            (source !== "installed" && !replace) ||
            (source === "app-store" && !status.appleEnabled)
          }
        >
          {busy ? "Working…" : "Add to queue"}
          <span>↗</span>
        </button>
      </form>
      <div className="request-note">
        <span>◷</span> Jobs run one at a time. Progress is saved automatically.
      </div>
    </section>
  );
}
