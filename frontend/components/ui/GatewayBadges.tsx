import type { Condition } from "@/lib/gateway-types.ts";

export function ConditionBadge({ condition }: { condition: Condition }) {
  const colorClass = condition.status === "True"
    ? "text-success bg-success/10"
    : condition.status === "False"
    ? "text-danger bg-danger/10"
    : "text-warning bg-warning/10";

  return (
    <span
      class={`inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium ${colorClass}`}
    >
      {condition.type}: {condition.status}
    </span>
  );
}

export function ProtocolBadge({ protocol }: { protocol: string }) {
  const colorClass = protocol === "HTTPS" || protocol === "TLS"
    ? "text-success bg-success/10"
    : protocol === "HTTP"
    ? "text-brand bg-brand/10"
    : "text-text-secondary bg-surface";

  return (
    <span
      class={`inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium ${colorClass}`}
    >
      {protocol}
    </span>
  );
}

export function StatusBadge(
  { conditions }: { conditions?: Condition[] },
) {
  if (!conditions || conditions.length === 0) {
    return <span class="text-text-muted text-sm">-</span>;
  }

  // Find the most relevant condition (Accepted or Programmed)
  const primary = conditions.find(
    (c) => c.type === "Accepted" || c.type === "Programmed",
  );

  if (!primary) {
    return <span class="text-text-muted text-sm">Unknown</span>;
  }

  const isHealthy = primary.status === "True";
  return (
    <span class={`text-sm ${isHealthy ? "text-success" : "text-danger"}`}>
      {isHealthy ? primary.type : primary.reason || "Not Ready"}
    </span>
  );
}
