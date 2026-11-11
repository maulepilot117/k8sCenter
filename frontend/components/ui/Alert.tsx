import type { ReactNode } from "react";

type AlertVariant = "error" | "warning" | "info" | "success";

interface AlertProps {
  variant?: AlertVariant;
  children: ReactNode;
  className?: string;
}

const VARIANT_STYLES: Record<AlertVariant, Record<string, string>> = {
  error: { background: "var(--error-dim)", color: "var(--error)" },
  warning: { background: "var(--warning-dim)", color: "var(--warning)" },
  info: { background: "var(--accent-dim)", color: "var(--accent)" },
  success: { background: "var(--success-dim)", color: "var(--success)" },
};

export function Alert({ variant = "error", children, className }: AlertProps) {
  return (
    <div
      className={`rounded-md px-4 py-3 text-sm ${className ?? ""}`}
      style={VARIANT_STYLES[variant]}
    >
      {children}
    </div>
  );
}
