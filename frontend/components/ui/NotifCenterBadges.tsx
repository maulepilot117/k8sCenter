import {
  type AppNotification,
  NOTIF_SEVERITY_COLORS,
  type NotifSeverity,
  type NotifSource,
  SOURCE_LABELS,
} from "@/lib/notif-center-types.ts";
import { LOCAL_CLUSTER_ID } from "@/src/lib/cluster.ts";

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
  // Not --success: a source badge on "Rubrik snapshot failed" must not read
  // as a good outcome; the severity dot carries the outcome.
  rubrik: "var(--accent-secondary)",
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

/**
 * The line under a Rubrik notification's title: its namespace, its cluster
 * when not the local one, and its message. Rubrik titles are fixed and name
 * nothing (R26), so without this line every failed snapshot looks alike and
 * a monitoring gap's length and cutoff are never shown (R24). Showing the
 * namespace is safe: the feed returns a Rubrik row only to admins and holders
 * of that namespace's Rubrik read grant (KTD11). Other sources render nothing.
 */
export function NotifContext({ n }: { n: AppNotification }) {
  if (n.source !== "rubrik") return null;
  const where: string[] = [];
  if (n.resourceNamespace) where.push(`Namespace ${n.resourceNamespace}`);
  if (n.clusterId && n.clusterId !== LOCAL_CLUSTER_ID) {
    where.push(`cluster ${n.clusterId}`);
  }
  if (where.length === 0 && !n.message) return null;
  return (
    <div
      data-testid="notif-context"
      // break-words: a namespace is up to 63 characters with no hyphen to
      // break at, wider than the bell's text column; it must wrap, not
      // scroll the dropdown sideways.
      className="mt-0.5 break-words text-xs font-normal text-text-muted"
    >
      {where.length > 0 && <span>{where.join(", ")}. </span>}
      {n.message}
    </div>
  );
}
