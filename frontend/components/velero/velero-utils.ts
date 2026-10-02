import type { StatusValue } from "@/components/ui/StatusDot.tsx";
import { type BackupOutcome, getPhaseCategory } from "@/lib/velero-types.ts";

/** Map a backend backup outcome → canonical StatusDot status. */
export function outcomeTone(outcome: BackupOutcome | undefined): StatusValue {
  switch (outcome) {
    case "succeeded":
      return "success";
    case "failed":
      return "error";
    case "inProgress":
      return "info";
    default:
      return "neutral";
  }
}

/** Map Velero phase string → canonical StatusDot status. */
export function phaseTone(phase: string): StatusValue {
  const cat = getPhaseCategory(phase);
  if (cat === "success") return "success";
  if (cat === "error") return "error";
  if (cat === "warning") return "warning";
  if (cat === "progress") return "info";
  return "neutral";
}
