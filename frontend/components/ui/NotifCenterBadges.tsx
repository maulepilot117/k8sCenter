/** @jsxImportSource react */
import {
  NOTIF_SEVERITY_COLORS,
  type NotifSeverity,
  type NotifSource,
  SOURCE_LABELS,
} from "@/lib/notif-center-types.ts";

/** Per-source color map for visual differentiation. */
const SOURCE_COLORS: Record<NotifSource, string> = {
  alert: "var(--error)",
  policy: "var(--warning)",
  gitops: "var(--accent)",
  diagnostic: "var(--error)",
  scan: "var(--warning)",
  cluster: "var(--success)",
  audit: "var(--text-muted)",
  limits: "var(--warning)",
  velero: "var(--accent)",
  certmanager: "var(--success)",
  external_secrets: "var(--accent-secondary)",
};

/**
 * Colored dot indicating notification severity. Lists that track read state
 * pass `read`: a read notification shows no dot at all (the space is kept so
 * rows stay aligned), so a dot always means unread.
 */
export function SeverityDot({
  severity,
  read,
}: {
  severity: NotifSeverity;
  read?: boolean;
}) {
  const color = NOTIF_SEVERITY_COLORS[severity] ?? "var(--text-muted)";
  return (
    <span
      data-read={read ? "true" : undefined}
      aria-hidden={read ? "true" : undefined}
      style={{
        display: "inline-block",
        width: "8px",
        height: "8px",
        borderRadius: "50%",
        backgroundColor: read ? "transparent" : color,
        flexShrink: 0,
      }}
      title={read ? undefined : `${severity} (unread)`}
    />
  );
}

/** Tinted pill badge for notification source with per-source coloring. */
export function SourceBadge({ source }: { source: NotifSource }) {
  const label = SOURCE_LABELS[source] ?? source;
  const color = SOURCE_COLORS[source] ?? "var(--text-muted)";
  return (
    <span
      className="inline-flex items-center px-1.5 py-0.5 rounded text-xs font-medium"
      style={{
        color,
        backgroundColor: `color-mix(in srgb, ${color} 12%, transparent)`,
      }}
    >
      {label}
    </span>
  );
}
