import { useState } from "react";
import { submitAuthCode } from "../../api/jobs";
import type { RunAction } from "../../hooks/useAction";
export function AuthCodeForm({
  jobId,
  busy,
  action,
}: {
  jobId: string;
  busy: boolean;
  action: RunAction;
}) {
  const [code, setCode] = useState("");
  return (
    <form
      className="auth-form"
      onSubmit={(e) => {
        e.preventDefault();
        const submittedCode = code;
        setCode("");
        void action(
          () => submitAuthCode(jobId, submittedCode),
          "Authentication code submitted.",
        );
      }}
    >
      <label htmlFor="auth-code">Apple verification code</label>
      <p className="muted">
        Enter the six-digit code from your trusted Apple device. It is used only
        for this challenge.
      </p>
      <input
        id="auth-code"
        inputMode="numeric"
        autoComplete="one-time-code"
        pattern="[0-9]{6}"
        maxLength={6}
        value={code}
        onChange={(e) => setCode(e.target.value)}
        required
      />
      <button type="submit" className="secondary" disabled={busy}>
        Submit code
      </button>
    </form>
  );
}
