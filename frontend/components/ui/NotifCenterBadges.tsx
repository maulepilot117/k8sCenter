import {
  type NotifSeverity,
  type NotifSource,
  SEVERITY_COLORS,
  SOURCE_LABELS,
} from "@/lib/notif-center-types.ts";

/** Colored dot indicating notification severity. */
export function SeverityDot({ severity }: { severity: NotifSeverity }) {
  const color = SEVERITY_COLORS[severity] ?? "var(--text-muted)";
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

/** Tinted pill badge for notification source. */
export function SourceBadge({ source }: { source: NotifSource }) {
  const label = SOURCE_LABELS[source] ?? source;
  const color = "var(--text-muted)";
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
