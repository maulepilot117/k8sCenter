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
}

/** Radial progress ring. Used for cluster health, utilization headlines. */
export default function Gauge(
  {
    value,
    size = 132,
    thickness = 11,
    color = "var(--success)",
    track = "var(--border-subtle)",
    label,
    sublabel,
  }: GaugeProps,
) {
  const cx = size / 2;
  const r = cx - thickness / 2 - 1;
  const circ = 2 * Math.PI * r;
  const pct = Math.max(0, Math.min(100, value));
  const offset = circ * (1 - pct / 100);

  return (
    <div
      style={{
        position: "relative",
        width: `${size}px`,
        height: `${size}px`,
        flexShrink: 0,
      }}
    >
      <svg width={size} height={size} viewBox={`0 0 ${size} ${size}`}>
        <circle
          cx={cx}
          cy={cx}
          r={r}
          fill="none"
          stroke={track}
          stroke-width={thickness}
        />
        <circle
          cx={cx}
          cy={cx}
          r={r}
          fill="none"
          stroke={color}
          stroke-width={thickness}
          stroke-linecap="round"
          stroke-dasharray={circ.toFixed(1)}
          stroke-dashoffset={offset.toFixed(1)}
          transform={`rotate(-90 ${cx} ${cx})`}
        />
      </svg>
      <div
        style={{
          position: "absolute",
          inset: 0,
          display: "flex",
          flexDirection: "column",
          alignItems: "center",
          justifyContent: "center",
        }}
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
      </div>
    </div>
  );
}
