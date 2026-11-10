import RadialGauge from "./RadialGauge.tsx";

interface GaugeProps {
  /** 0..100 */
  value: number;
  size?: number;
  thickness?: number;
  color?: string;
  track?: string;
  /** big center text, e.g. "98%" */
  label?: string;
  /** small caption under the label, e.g. "HEALTHY" */
  sublabel?: string;
  /** What the gauge measures, e.g. "Cluster health"; leads the accessible label. */
  name?: string;
}

/** Radial progress ring. Used for cluster health, utilization headlines. */
export default function Gauge({
  value,
  size = 132,
  thickness = 11,
  color = "var(--success)",
  track = "var(--border-subtle)",
  label,
  sublabel,
  name,
}: GaugeProps) {
  const pct = Math.max(0, Math.min(100, value));
  const shown = label ?? `${Math.round(pct)}%`;
  const accessibleLabel = [name ? `${name}: ${shown}` : shown, sublabel]
    .filter(Boolean)
    .join(", ");

  return (
    <RadialGauge
      percent={pct}
      size={size}
      thickness={thickness}
      color={color}
      track={track}
      label={accessibleLabel}
    >
      {label && (
        <span
          style={{
            fontSize: "30px",
            fontWeight: 750,
            letterSpacing: "-0.02em",
            color: "var(--text-primary)",
          }}
        >
          {label}
        </span>
      )}
      {sublabel && (
        <span
          style={{
            fontSize: "11px",
            fontWeight: 600,
            color,
            textTransform: "uppercase",
            letterSpacing: "0.05em",
          }}
        >
          {sublabel}
        </span>
      )}
    </RadialGauge>
  );
}
