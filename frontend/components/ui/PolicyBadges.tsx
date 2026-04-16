import { ColorBadge } from "@/components/ui/ColorBadge.tsx";
export { ColorBadge } from "@/components/ui/ColorBadge.tsx";
export { SEVERITY_COLORS, SEVERITY_ORDER } from "@/lib/badge-colors.ts";
import { SEVERITY_COLORS } from "@/lib/badge-colors.ts";

export const ENGINE_COLORS: Record<string, string> = {
  kyverno: "var(--success)",
  gatekeeper: "var(--accent)",
};

export const ACTION_LABELS: Record<string, { label: string; color: string }> = {
  denied: { label: "Denied", color: "var(--danger)" },
  warned: { label: "Warned", color: "var(--warning)" },
  audited: { label: "Audited", color: "var(--text-muted)" },
};

export function SeverityBadge({ severity }: { severity: string }) {
  return (
    <ColorBadge
      label={severity}
      color={SEVERITY_COLORS[severity] ?? "var(--text-muted)"}
    />
  );
}

export function EngineBadge({ engine }: { engine: string }) {
  return (
    <ColorBadge
      label={engine}
      color={ENGINE_COLORS[engine] ?? "var(--text-muted)"}
    />
  );
}

export function BlockingBadge({ blocking }: { blocking: boolean }) {
  const color = blocking ? "var(--danger)" : "var(--text-muted)";
  const label = blocking ? "Enforce" : "Audit";
  const title = blocking ? "Blocks admission" : "Audit only";
  return (
    <span
      class="inline-flex items-center gap-1 px-2 py-0.5 rounded text-xs font-medium"
      style={{
        color,
        backgroundColor: `color-mix(in srgb, ${color} 15%, transparent)`,
      }}
      title={title}
    >
      {label}
    </span>
  );
}

export function ActionBadge({ action }: { action: string }) {
  const info = ACTION_LABELS[action] ?? {
    label: action,
    color: "var(--text-muted)",
  };
  return <ColorBadge label={info.label} color={info.color} />;
}
