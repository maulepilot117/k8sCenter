import { getPhaseCategory } from "@/lib/velero-types.ts";
import type { Tone } from "@/components/ui/glass/StatusBadge.tsx";

/** Map Velero phase string → StatusBadge tone. */
export function phaseTone(phase: string): Tone {
  const cat = getPhaseCategory(phase);
  if (cat === "success") return "ok";
  if (cat === "error") return "crit";
  if (cat === "warning") return "warn";
  if (cat === "progress") return "info";
  return "neutral";
}
