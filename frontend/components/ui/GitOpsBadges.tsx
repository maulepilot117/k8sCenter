import type { HealthStatus, SyncStatus } from "@/lib/gitops-types.ts";

/** Shared color maps for GitOps UI */
export const TOOL_COLORS: Record<string, string> = {
  argocd: "var(--warning)",
  fluxcd: "var(--accent)",
};

export const SYNC_COLORS: Record<string, string> = {
  synced: "var(--success)",
  outofsync: "var(--danger)",
  progressing: "var(--accent)",
  stalled: "var(--warning)",
  failed: "var(--danger)",
  unknown: "var(--text-muted)",
};

export const HEALTH_COLORS: Record<string, string> = {
  healthy: "var(--success)",
  degraded: "var(--danger)",
  progressing: "var(--accent)",
  suspended: "var(--text-muted)",
  unknown: "var(--text-muted)",
};

/** Generic tinted badge — color text on a color-mix background. */
function ColorBadge(
  { label, color }: { label: string; color: string },
) {
  return (
    <span
      class="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium"
      style={{
        color,
        backgroundColor: `color-mix(in srgb, ${color} 15%, transparent)`,
      }}
    >
      {label}
    </span>
  );
}

export function ToolBadge({ tool }: { tool: string }) {
  const labels: Record<string, string> = {
    argocd: "Argo CD",
    fluxcd: "Flux",
  };
  return (
    <ColorBadge
      label={labels[tool] ?? tool}
      color={TOOL_COLORS[tool] ?? "var(--text-muted)"}
    />
  );
}

export function SyncStatusBadge({ status }: { status: SyncStatus }) {
  const labels: Record<string, string> = {
    synced: "Synced",
    outofsync: "Out of Sync",
    progressing: "Progressing",
    stalled: "Stalled",
    failed: "Failed",
    unknown: "Unknown",
  };
  return (
    <ColorBadge
      label={labels[status] ?? status}
      color={SYNC_COLORS[status] ?? "var(--text-muted)"}
    />
  );
}

export function HealthStatusBadge({ status }: { status: HealthStatus }) {
  const labels: Record<string, string> = {
    healthy: "Healthy",
    degraded: "Degraded",
    progressing: "Progressing",
    suspended: "Suspended",
    unknown: "Unknown",
  };
  return (
    <ColorBadge
      label={labels[status] ?? status}
      color={HEALTH_COLORS[status] ?? "var(--text-muted)"}
    />
  );
}
