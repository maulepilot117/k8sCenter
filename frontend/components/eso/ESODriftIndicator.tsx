import { DriftBadge } from "@/components/eso/ESOBadges.tsx";
import type { DriftStatus, DriftUnknownReason } from "@/lib/eso-types.ts";

/** Per-reason hint text rendered next to the badge. The reason field is
 * populated only on the detail endpoint; list rows leave driftStatus as
 * Unknown without a reason. */
const REASON_HINTS: Record<DriftUnknownReason, string> = {
  no_synced_rv:
    "ESO has not synced this ExternalSecret yet — there is nothing to compare against.",
  no_data_hash:
    "The synced Secret has no ESO data hash, so its data cannot be checked against what ESO wrote.",
  no_target_name:
    "ExternalSecret has no target Secret name — nothing to compare against.",
  secret_deleted:
    "The synced Secret has been deleted. ESO should recreate it on the next reconcile.",
  rbac_denied:
    "Drift requires `get secret` on the target namespace. Your role does not include that permission.",
  transient_error: "Drift check failed transiently — retry the page.",
  client_error: "Internal error creating the impersonated client.",
};

interface ESODriftIndicatorProps {
  status: DriftStatus;
  reason?: DriftUnknownReason;
}

/** Tri-state drift indicator for the ExternalSecret detail page. Renders the
 * coloured badge inline with explanatory text. When status=Drifted it points
 * at the page's Force sync button, which re-reconciles the Secret from the
 * provider and so reverts the edit. */
export function ESODriftIndicator({ status, reason }: ESODriftIndicatorProps) {
  return (
    <div className="flex flex-col gap-1.5">
      <div className="flex items-center gap-2">
        <DriftBadge status={status} />
        {status === "Drifted" && (
          <span className="text-xs text-text-muted">
            The synced Secret has been edited since ESO last reconciled it.
            Force sync rewrites it from the provider.
          </span>
        )}
        {status === "Unknown" && reason && (
          <span className="text-xs text-text-muted">
            {REASON_HINTS[reason] ?? "Drift state is currently unknown."}
          </span>
        )}
        {status === "InSync" && (
          <span className="text-xs text-text-muted">
            The synced Secret's data matches the hash ESO recorded when it last
            wrote it.
          </span>
        )}
      </div>
    </div>
  );
}
