import RadialGauge from "@/components/charts/RadialGauge.tsx";

interface GaugeRingProps {
  value: number; // 0-100
  size?: number;
  strokeWidth?: number;
  color: string;
  secondaryColor?: string;
  label?: string;
  displayValue?: string; // Override the default "{value}%" display
  valueSize?: string; // e.g., "42px" — override font size
  valueGradient?: boolean; // Use gradient text
  // When true, renders the ring fully filled in `color` regardless of `value`,
  // skips the gradient, and shows a dash for the value (used for the "unknown"
  // health status — a null score would otherwise draw an empty ring).
  indeterminate?: boolean;
}

export function GaugeRing({
  value,
  size = 80,
  strokeWidth = 6,
  color,
  secondaryColor,
  label,
  displayValue,
  valueSize,
  valueGradient,
  indeterminate = false,
}: GaugeRingProps) {
  const clampedValue = Math.max(0, Math.min(100, value));
  const shown =
    displayValue ?? (indeterminate ? "—" : `${Math.round(clampedValue)}%`);
  const accessibleValue =
    indeterminate && displayValue === undefined ? "value not known" : shown;
  const accessibleLabel = label
    ? `${label}: ${accessibleValue}`
    : accessibleValue;

  return (
    <RadialGauge
      percent={indeterminate ? 100 : clampedValue}
      size={size}
      thickness={strokeWidth}
      color={color}
      secondaryColor={indeterminate ? undefined : secondaryColor}
      track="var(--bg-elevated)"
      label={accessibleLabel}
    >
      <span
        style={{
          fontSize: valueSize ?? "14px",
          fontWeight: valueSize ? 700 : 600,
          fontFamily: "var(--font-mono, monospace)",
          ...(valueGradient
            ? {
                background:
                  "linear-gradient(135deg, var(--accent), var(--accent-secondary))",
                WebkitBackgroundClip: "text",
                WebkitTextFillColor: "transparent",
              }
            : { color: "var(--text-primary)" }),
        }}
      >
        {shown}
      </span>
      {label && (
        <span className="text-xs" style={{ color: "var(--text-muted)" }}>
          {label}
        </span>
      )}
    </RadialGauge>
  );
}
