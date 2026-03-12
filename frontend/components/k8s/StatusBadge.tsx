import { Badge } from "@/components/ui/Badge.tsx";
import type { PodPhase } from "@/lib/k8s-types.ts";

type BadgeVariant = "success" | "warning" | "danger" | "info" | "neutral";

/** Map k8s resource states to consistent badge variants. */
const statusMap: Record<string, BadgeVariant> = {
  // Pod phases
  Running: "success",
  Succeeded: "success",
  Pending: "warning",
  Failed: "danger",
  Unknown: "neutral",
  // Node conditions
  Ready: "success",
  NotReady: "danger",
  // Deployment
  Available: "success",
  Progressing: "info",
  Unavailable: "danger",
  // Generic
  Active: "success",
  Terminating: "warning",
  Bound: "success",
  Released: "warning",
  Lost: "danger",
};

interface StatusBadgeProps {
  status: string;
  class?: string;
}

export function StatusBadge({ status, class: className }: StatusBadgeProps) {
  const variant = statusMap[status] ?? "neutral";
  return <Badge variant={variant} class={className}>{status}</Badge>;
}

/** Convenience for pod phase. */
export function PodStatusBadge({ phase }: { phase: PodPhase }) {
  return <StatusBadge status={phase} />;
}
