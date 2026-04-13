import {
  NOTIF_SEVERITY_COLORS,
  type NotifSeverity,
  type NotifSource,
  SOURCE_LABELS,
} from "@/lib/notif-center-types.ts";

/** Per-source color map for visual differentiation. */
const SOURCE_COLORS: Record<NotifSource, string> = {
  alert: "var(--danger)",
  policy: "var(--warning)",
  gitops: "var(--accent)",
  diagnostic: "var(--danger)",
  scan: "var(--warning)",
  cluster: "var(--success)",
  audit: "var(--text-muted)",
  limits: "var(--warning)",
  velero: "var(--accent)",
  certmanager: "var(--success)",
};

/** Colored dot indicating notification severity. */
export function SeverityDot({ severity }: { severity: NotifSeverity }) {
  const color = NOTIF_SEVERITY_COLORS[severity] ?? "var(--text-muted)";
  return (
    <span
      style={{
        display: "inline-block",
        width: "8px",
        height: "8px",
        borderRadius: "50%",
        backgroundColor: color,
        flexShrink: 0,
      }}
      title={severity}
    />
  );
}

/** Tinted pill badge for notification source with per-source coloring. */
export function SourceBadge({ source }: { source: NotifSource }) {
  const label = SOURCE_LABELS[source] ?? source;
  const color = SOURCE_COLORS[source] ?? "var(--text-muted)";
  return (
    <span
      class="inline-flex items-center px-1.5 py-0.5 rounded text-xs font-medium"
      style={{
        color,
        backgroundColor: `color-mix(in srgb, ${color} 12%, transparent)`,
      }}
    >
      {label}
    </span>
  );
}
