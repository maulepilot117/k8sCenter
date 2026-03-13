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
    "bg-green-50 text-green-700 ring-green-600/20 dark:bg-green-500/10 dark:text-green-400 dark:ring-green-500/20",
  warning:
    "bg-amber-50 text-amber-700 ring-amber-600/20 dark:bg-amber-500/10 dark:text-amber-400 dark:ring-amber-500/20",
  danger:
    "bg-red-50 text-red-700 ring-red-600/20 dark:bg-red-500/10 dark:text-red-400 dark:ring-red-500/20",
  info:
    "bg-blue-50 text-blue-700 ring-blue-600/20 dark:bg-blue-500/10 dark:text-blue-400 dark:ring-blue-500/20",
  neutral:
    "bg-slate-50 text-slate-600 ring-slate-500/20 dark:bg-slate-500/10 dark:text-slate-400 dark:ring-slate-500/20",
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
