import StatusBadge from "@/components/ui/glass/StatusBadge.tsx";
import { executionBadge, verificationBadge } from "@/lib/change-copy.ts";
import type { ReceiptState, VerificationState } from "@/lib/change-types.ts";

/**
 * The two independent facts about a change, as two badges: what the API
 * server accepted (execution) and whether the result was observed to hold
 * (verification). They are never merged into one "success" — `applied` with
 * `pending` reads "Applied · Verification pending".
 */
export function ChangeStateBadges({
  state,
  verification,
}: {
  state: ReceiptState;
  verification: VerificationState;
}) {
  const e = executionBadge(state);
  const v = verificationBadge(verification);
  return (
    <span class="inline-flex flex-wrap items-center gap-1.5">
      <StatusBadge label={e.label} tone={e.tone} />
      <span aria-hidden="true" class="text-text-muted">
        ·
      </span>
      <StatusBadge label={v.label} tone={v.tone} />
    </span>
  );
}
