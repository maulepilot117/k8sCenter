interface SparklineProps {
  data: number[];
  width?: number;
  height?: number;
  stroke?: string;
  /** area fill below the line (e.g. "color-mix(in srgb, var(--accent) 16%, transparent)") */
  fill?: string;
  strokeWidth?: number;
  /** true (default) = min/max scale to fit; false = treat values as 0..100 */
  normalize?: boolean;
}

/** Responsive single-series sparkline. Scales to its container width. */
export default function Sparkline(
  {
    data,
    width = 130,
    height = 34,
    stroke = "var(--accent)",
    fill,
    strokeWidth = 1.8,
    normalize = true,
  }: SparklineProps,
) {
  if (!data.length) return null;
  const pad = 3;
  const n = data.length;
  const min = normalize ? Math.min(...data) : 0;
  const max = normalize ? Math.max(...data) : 100;
  const range = (max - min) || 1;
  const x = (i: number) => pad + (i * (width - 2 * pad)) / (n - 1);
  const y = (v: number) =>
    height - pad - ((v - min) / range) * (height - 2 * pad);
  const line = data.map((v, i) =>
    `${i ? "L" : "M"}${x(i).toFixed(1)} ${y(v).toFixed(1)}`
  ).join(" ");
  const area = `${line} L${x(n - 1).toFixed(1)} ${height - pad} L${
    x(0).toFixed(1)
  } ${height - pad} Z`;

  return (
    <svg
      width="100%"
      height={height}
      viewBox={`0 0 ${width} ${height}`}
      preserveAspectRatio="none"
      style={{ display: "block" }}
    >
      {fill && <path d={area} fill={fill} />}
      <path
        d={line}
        fill="none"
        stroke={stroke}
        stroke-width={strokeWidth}
        stroke-linecap="round"
        stroke-linejoin="round"
      />
    </svg>
  );
}
