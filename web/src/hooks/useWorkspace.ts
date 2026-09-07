import { useCallback, useEffect, useRef, useState } from "react";
import { loadWorkspace } from "../api/jobs";
import type { Job, Status } from "../types/api";

const POLL_INTERVAL_MS = 2000;
/** Persisted snapshots are polled without overlap; pending reads abort on unmount. */
export function useWorkspace() {
  const [jobs, setJobs] = useState<Job[]>([]);
  const [status, setStatus] = useState<Status>();
  const [error, setError] = useState("");
  const controller = useRef<AbortController | null>(null);
  const sequence = useRef(0);
  const refresh = useCallback(async () => {
    const signal = controller.current?.signal;
    if (!signal || signal.aborted) return;
    const request = ++sequence.current;
    const next = await loadWorkspace(signal);
    if (signal.aborted || request !== sequence.current) return;
    setJobs(next.jobs);
    setStatus(next.status);
    setError("");
  }, []);
  useEffect(() => {
    const current = new AbortController();
    controller.current = current;
    let timer: ReturnType<typeof setTimeout>;
    const poll = async () => {
      try {
        await refresh();
      } catch {
        if (!current.signal.aborted)
          setError("Cannot reach ipa-now. Check that the backend is running.");
      } finally {
        if (!current.signal.aborted) timer = setTimeout(poll, POLL_INTERVAL_MS);
      }
    };
    void poll();
    return () => {
      current.abort();
      clearTimeout(timer);
    };
  }, [refresh]);
  return { jobs, status, error, refresh, dismissError: () => setError("") };
}
