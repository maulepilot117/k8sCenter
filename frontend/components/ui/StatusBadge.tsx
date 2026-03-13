import { statusVariant, VARIANT_CLASSES } from "@/lib/status-colors.ts";
import type { StatusVariant } from "@/lib/status-colors.ts";

interface StatusBadgeProps {
  status: string;
  variant?: StatusVariant;
}

export function StatusBadge({ status, variant }: StatusBadgeProps) {
  const v = variant ?? statusVariant(status);
  return (
    <span
      class={`inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium ring-1 ring-inset ${
        VARIANT_CLASSES[v]
      }`}
    >
      {status}
    </span>
  );
}
