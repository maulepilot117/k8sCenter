interface SummaryRingProps {
  value: number | string;
  max: number;
  size?: number;
  color: string;
}

export function SummaryRing(
  { value, max, size = 40, color }: SummaryRingProps,
) {
  const strokeWidth = 4;
  const radius = (size - strokeWidth) / 2;
  const circumference = 2 * Math.PI * radius;
  const numericValue = typeof value === "string" ? parseFloat(value) : value;
  const pct = max > 0 ? Math.min(numericValue / max, 1) : 0;
  const offset = circumference - pct * circumference;
  const center = size / 2;

  return (
    <div
      style={{
        width: `${size}px`,
        height: `${size}px`,
        position: "relative",
        flexShrink: 0,
      }}
    >
      <svg
        width={size}
        height={size}
        viewBox={`0 0 ${size} ${size}`}
        style={{ transform: "rotate(-90deg)" }}
      >
        {/* Track */}
        <circle
          cx={center}
          cy={center}
          r={radius}
          fill="none"
          stroke="var(--border-primary)"
          stroke-width={strokeWidth}
        />
        {/* Progress */}
        <circle
          cx={center}
          cy={center}
          r={radius}
          fill="none"
          stroke={color}
          stroke-width={strokeWidth}
          stroke-linecap="round"
          stroke-dasharray={circumference}
          stroke-dashoffset={offset}
          style={{ transition: "stroke-dashoffset 1s ease" }}
        />
      </svg>
      <div
        style={{
          position: "absolute",
          inset: 0,
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          fontSize: "10px",
          fontWeight: 600,
          fontFamily: "var(--font-mono)",
          color: color,
        }}
      >
        {value}
      </div>
    </div>
  );
}
