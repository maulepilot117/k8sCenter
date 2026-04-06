/** Shared color maps for policy UI */
export const SEVERITY_COLORS: Record<string, string> = {
  critical: "var(--danger)",
  high: "var(--warning)",
  medium: "var(--accent)",
  low: "var(--text-muted)",
};

export const ENGINE_COLORS: Record<string, string> = {
  kyverno: "var(--success)",
  gatekeeper: "var(--accent)",
};

export const ACTION_LABELS: Record<string, { label: string; color: string }> = {
  denied: { label: "Denied", color: "var(--danger)" },
  warned: { label: "Warned", color: "var(--warning)" },
  audited: { label: "Audited", color: "var(--text-muted)" },
};

export const SEVERITY_ORDER = ["critical", "high", "medium", "low"] as const;

/** Generic tinted badge — color text on a color-mix background. */
export function ColorBadge(
  { label, color }: { label: string; color: string },
) {
  return (
    <span
      class="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium"
      style={{
        color,
        backgroundColor: `color-mix(in srgb, ${color} 15%, transparent)`,
      }}
    >
      {label}
    </span>
  );
}

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
