import { getPhaseCategory } from "@/lib/velero-types.ts";
import type { StatusValue } from "@/components/ui/StatusDot.tsx";

/** Map Velero phase string → canonical StatusDot status. */
export function phaseTone(phase: string): StatusValue {
  const cat = getPhaseCategory(phase);
  if (cat === "success") return "success";
  if (cat === "error") return "error";
  if (cat === "warning") return "warning";
  if (cat === "progress") return "info";
  return "neutral";
}
