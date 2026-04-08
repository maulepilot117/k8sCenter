import { ColorBadge } from "@/components/ui/PolicyBadges.tsx";

/** Color map for notification provider/alert/receiver status. */
export const STATUS_COLORS: Record<string, string> = {
  ready: "var(--success)",
  "not ready": "var(--error)",
  suspended: "var(--warning)",
  unknown: "var(--text-secondary)",
};

/** Color map for event severity. */
export const SEVERITY_COLORS: Record<string, string> = {
  info: "var(--accent)",
  error: "var(--error)",
};

export function StatusBadge({ status }: { status: string }) {
  const key = status.toLowerCase();
  const labels: Record<string, string> = {
    ready: "Ready",
    "not ready": "Not Ready",
    suspended: "Suspended",
    unknown: "Unknown",
  };
  return (
    <ColorBadge
      label={labels[key] ?? status}
      color={STATUS_COLORS[key] ?? "var(--text-secondary)"}
    />
  );
}

export function SeverityBadge({ severity }: { severity: string }) {
  const key = severity.toLowerCase();
  return (
    <ColorBadge
      label={severity}
      color={SEVERITY_COLORS[key] ?? "var(--text-secondary)"}
    />
  );
}

export function ProviderTypeBadge({ type }: { type: string }) {
  return <ColorBadge label={type} color="var(--accent)" />;
}
