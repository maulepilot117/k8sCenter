/**
 * Shared status-to-variant mapping for Kubernetes resource statuses.
 * Used by both StatusBadge component and resource-columns inline badges
 * to ensure consistent color semantics across the UI.
 */

export type StatusVariant =
  | "success"
  | "warning"
  | "danger"
  | "info"
  | "neutral";

export const VARIANT_CLASSES: Record<StatusVariant, string> = {
  success:
    "bg-success/10 text-success ring-success/20",
  warning:
    "bg-warning/10 text-warning ring-warning/20",
  danger:
    "bg-danger/10 text-danger ring-danger/20",
  info:
    "bg-accent/10 text-accent ring-accent/20",
  neutral:
    "bg-elevated text-text-muted ring-border-primary/20",
};

const SUCCESS_STATUSES = new Set([
  "running",
  "active",
  "bound",
  "ready",
  "healthy",
  "available",
  "complete",
  "succeeded",
  "true",
]);

const WARNING_STATUSES = new Set([
  "pending",
  "waiting",
  "creating",
  "terminating",
  "warning",
]);

const DANGER_STATUSES = new Set([
  "failed",
  "error",
  "crashloopbackoff",
  "imagepullbackoff",
  "evicted",
  "oomkilled",
  "lost",
  "false",
]);

const NEUTRAL_STATUSES = new Set(["unknown", "not ready"]);

/** Maps a k8s status string to a semantic variant. */
export function statusVariant(status: string): StatusVariant {
  const s = status.toLowerCase();
  if (SUCCESS_STATUSES.has(s)) return "success";
  if (WARNING_STATUSES.has(s)) return "warning";
  if (DANGER_STATUSES.has(s)) return "danger";
  if (NEUTRAL_STATUSES.has(s)) return "neutral";
  return "info";
}

/** Returns Tailwind classes for a given status string. */
export function statusColor(status: string): string {
  return VARIANT_CLASSES[statusVariant(status)];
}

/** Returns inline style object for a given status string using CSS variables. */
export function statusStyle(status: string): Record<string, string> {
  const v = statusVariant(status);
  const map: Record<StatusVariant, Record<string, string>> = {
    success: { background: "var(--success-dim)", color: "var(--success)" },
    warning: { background: "var(--warning-dim)", color: "var(--warning)" },
    danger: { background: "var(--error-dim)", color: "var(--error)" },
    info: { background: "var(--accent-dim)", color: "var(--accent)" },
    neutral: { background: "var(--bg-elevated)", color: "var(--text-muted)" },
  };
  return map[v];
}
