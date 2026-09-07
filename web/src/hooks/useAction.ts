import { useState } from "react";
export type RunAction = (
  operation: () => Promise<unknown>,
  successMessage: string,
) => Promise<void>;
/** Shared mutation feedback; forms retain ownership of their values and resets. */
export function useAction(refresh: () => Promise<void>) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const action: RunAction = async (operation, successMessage) => {
    setBusy(true);
    setError("");
    setNotice("");
    try {
      await operation();
      await refresh();
      setNotice(successMessage);
    } catch (error) {
      setError(error instanceof Error ? error.message : "Request failed.");
    } finally {
      setBusy(false);
    }
  };
  return {
    busy,
    error,
    notice,
    action,
    dismissError: () => setError(""),
    dismissNotice: () => setNotice(""),
  };
}
