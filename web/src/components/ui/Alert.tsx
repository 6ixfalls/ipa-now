import type { ReactNode } from "react";

interface AlertProps {
  variant: "error" | "notice" | "warning";
  children: ReactNode;
  dismissLabel?: string;
  onDismiss?: () => void;
}
export function Alert({
  variant,
  children,
  dismissLabel,
  onDismiss,
}: AlertProps) {
  return (
    <div
      role={
        variant === "error"
          ? "alert"
          : variant === "notice"
            ? "status"
            : undefined
      }
      className={`alert ${variant}`}
    >
      {children}
      {onDismiss && (
        <button type="button" aria-label={dismissLabel} onClick={onDismiss}>
          ×
        </button>
      )}
    </div>
  );
}
