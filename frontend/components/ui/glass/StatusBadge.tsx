export type Tone = "ok" | "warn" | "crit" | "info" | "neutral";

const MAP: Record<Tone, { color: string; bg: string }> = {
  ok: {
    color: "var(--success)",
    bg: "color-mix(in srgb, var(--success) 14%, transparent)",
  },
  warn: {
    color: "var(--warning)",
    bg: "color-mix(in srgb, var(--warning) 14%, transparent)",
  },
  crit: {
    color: "var(--error)",
    bg: "color-mix(in srgb, var(--error) 14%, transparent)",
  },
  info: {
    color: "var(--info)",
    bg: "color-mix(in srgb, var(--info) 14%, transparent)",
  },
  neutral: { color: "var(--text-muted)", bg: "var(--bg-elevated)" },
};

/** Pill badge for resource status (Available / Degraded / Failed / …). */
export default function StatusBadge(
  { label, tone = "neutral" }: { label: string; tone?: Tone },
) {
  const t = MAP[tone];
  return (
    <span
      style={{
        fontSize: "11px",
        fontWeight: 650,
        padding: "3px 9px",
        borderRadius: "6px",
        color: t.color,
        background: t.bg,
        whiteSpace: "nowrap",
      }}
    >
      {label}
    </span>
  );
}

/** Small status dot for inline use in tables/nav. */
export function StatusDot(
  { tone = "neutral", size = 8 }: { tone?: Tone; size?: number },
) {
  return (
    <span
      style={{
        width: `${size}px`,
        height: `${size}px`,
        borderRadius: "50%",
        flexShrink: 0,
        display: "inline-block",
        background: MAP[tone].color,
      }}
    />
  );
}
