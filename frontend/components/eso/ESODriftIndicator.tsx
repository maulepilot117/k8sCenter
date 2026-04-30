import type { DriftStatus, DriftUnknownReason } from "@/lib/eso-types.ts";
import { DriftBadge } from "@/components/eso/ESOBadges.tsx";

/** Per-reason hint text rendered next to the badge. The reason field is
 * populated only on the detail endpoint; list rows leave driftStatus as
 * Unknown without a reason. */
const REASON_HINTS: Record<DriftUnknownReason, string> = {
  no_synced_rv:
    "Provider does not expose syncedResourceVersion — drift cannot be determined.",
  no_target_name:
    "ExternalSecret has no target Secret name — nothing to compare against.",
  secret_deleted:
    "The synced Secret has been deleted. ESO should recreate it on the next reconcile.",
  rbac_denied:
    "Drift requires `get secret` on the target namespace. Your role does not include that permission.",
  transient_error:
    "Drift check failed transiently — retry the page.",
  client_error: "Internal error creating the impersonated client.",
};

interface ESODriftIndicatorProps {
  status: DriftStatus;
  reason?: DriftUnknownReason;
  /** When provided, the Drifted state renders a "Revert" button stub. The
   * actual force-sync action lands in Phase E (Unit 14); this Phase B
   * variant is non-functional and shows an inline note. */
  onRevert?: () => void;
}

/** Tri-state drift indicator for the ExternalSecret detail page. Renders the
 * coloured badge inline with explanatory text and (when status=Drifted) a
 * Revert action stub. */
export function ESODriftIndicator(
  { status, reason, onRevert }: ESODriftIndicatorProps,
) {
  return (
    <div class="flex flex-col gap-1.5">
      <div class="flex items-center gap-2">
        <DriftBadge status={status} />
        {status === "Drifted" && (
          <span class="text-xs text-text-muted">
            The synced Secret has been edited since ESO last reconciled it.
          </span>
        )}
        {status === "Unknown" && reason && (
          <span class="text-xs text-text-muted">
            {REASON_HINTS[reason] ??
              "Drift state is currently unknown."}
          </span>
        )}
        {status === "InSync" && (
          <span class="text-xs text-text-muted">
            Synced Secret matches the resource version ESO recorded.
          </span>
        )}
      </div>
      {status === "Drifted" && onRevert && (
        <div class="flex items-center gap-2">
          <button
            type="button"
            onClick={onRevert}
            class="text-xs px-2 py-1 rounded border border-border-primary text-text-muted hover:text-text-primary hover:bg-bg-base transition-colors"
            disabled
            aria-disabled="true"
            title="Force-sync ships in Phase E"
          >
            Revert drift
          </button>
          <span class="text-[11px] text-text-muted">
            Force-sync action available in Phase&nbsp;E.
          </span>
        </div>
      )}
    </div>
  );
}
