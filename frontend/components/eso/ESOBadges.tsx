import type { DriftStatus, Status, ThresholdSource } from "@/lib/eso-types.ts";
import { ColorBadge } from "@/components/ui/ColorBadge.tsx";

/** Status enum → CSS custom property color. Six values cover the lifecycle:
 * Synced (success), SyncFailed (danger), Refreshing (accent), Stale (warning),
 * Drifted (accent-secondary — distinct from failure red), Unknown (muted). */
const STATUS_COLORS: Record<Status, string> = {
  Synced: "var(--success)",
  SyncFailed: "var(--danger)",
  Refreshing: "var(--accent)",
  Stale: "var(--warning)",
  Drifted: "var(--accent-secondary)",
  Unknown: "var(--text-muted)",
};

const DRIFT_COLORS: Record<DriftStatus, string> = {
  InSync: "var(--success)",
  Drifted: "var(--accent-secondary)",
  Unknown: "var(--text-muted)",
};

const SOURCE_LABELS: Record<ThresholdSource, string> = {
  default: "Default",
  externalsecret: "ExternalSecret",
  secretstore: "Store",
  clustersecretstore: "ClusterStore",
};

/** Renders a pill badge for ExternalSecret status. */
export function StatusBadge({ status }: { status: Status }) {
  return (
    <ColorBadge
      label={status}
      color={STATUS_COLORS[status] ?? "var(--text-muted)"}
    />
  );
}

/** Renders a pill badge for the tri-state drift indicator. Used inline on
 * detail pages alongside the status badge. List rows rely on the Status
 * badge alone (DriftStatus = "Drifted" is overlaid into Status by
 * DeriveStatus on the detail endpoint). */
export function DriftBadge({ status }: { status: DriftStatus }) {
  return (
    <ColorBadge
      label={status}
      color={DRIFT_COLORS[status] ?? "var(--text-muted)"}
    />
  );
}

/** Threshold source attribution badge — Phase D resolver populates these per
 * key (StaleAfterMinutesSource, AlertOnRecoverySource, etc.). Phase A response
 * shape carries the field but always omits the value, so renders nothing. */
export function SourceBadge({ source }: { source?: ThresholdSource }) {
  if (!source) return null;
  return (
    <span class="inline-flex items-center px-1.5 py-0.5 rounded text-[11px] font-normal text-text-muted bg-base border border-border-subtle">
      {SOURCE_LABELS[source] ?? source}
    </span>
  );
}

/** Provider family badge (vault, aws, gcp, azurekv, kubernetes, ...).
 * Uses muted styling — provider is informational, not status-driven. */
export function ProviderBadge({ provider }: { provider: string }) {
  if (!provider) {
    return <span class="text-xs text-text-muted">&mdash;</span>;
  }
  return <ColorBadge label={provider} color="var(--accent)" />;
}
