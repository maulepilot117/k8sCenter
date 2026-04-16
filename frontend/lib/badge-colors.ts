/** Shared severity color map for policy, scanning, and other severity-based UIs. */
export const SEVERITY_COLORS: Record<string, string> = {
  critical: "var(--danger)",
  high: "var(--warning)",
  medium: "var(--accent)",
  low: "var(--text-muted)",
  unknown: "var(--text-muted)",
};

export const SEVERITY_ORDER = ["critical", "high", "medium", "low"] as const;
