/** @jsxImportSource react */
import { useId } from "react";

interface SparklineChartProps {
  data: number[];
  color: string;
  /** Fixed pixel width. Omit to fill the parent container (default). */
  width?: number;
  height?: number;
}

// Logical coordinate width used for point math when the SVG is rendered
// fluid (width="100%"). The viewBox maps this to whatever pixel width the
// container provides; preserveAspectRatio="none" stretches it horizontally.
const VIEW_W = 100;

export function SparklineChart({
  data,
  color,
  width,
  height = 28,
}: SparklineChartProps) {
  const gradientId = `spark-${useId()}`;

  // Drop non-finite samples (NaN/±Infinity) so they cannot produce broken
  // `M x,NaN` path coordinates that render an invisible, silently-empty chart.
  const clean = data.filter((v) => Number.isFinite(v));
  if (clean.length < 2) return null;

  // When no explicit pixel width is given, render fluid: the SVG fills its
  // container width and the path coordinates are computed against VIEW_W,
  // then non-uniformly scaled to fit. vector-effect keeps the stroke crisp.
  const fluid = width === undefined;
  const w = fluid ? VIEW_W : width;

  const min = Math.min(...clean);
  const max = Math.max(...clean);
  const range = max - min || 1;

  const points = clean.map((v, i) => {
    const x = (i / (clean.length - 1)) * w;
    const y = height - ((v - min) / range) * (height - 2) - 1;
    return `${x},${y}`;
  });

  const polyline = points.join(" ");
  const areaPath = `M0,${height} L${points
    .map((p) => `${p}`)
    .join(" L")} L${w},${height} Z`;

  return (
    <svg
      width={fluid ? "100%" : width}
      height={height}
      viewBox={`0 0 ${w} ${height}`}
      preserveAspectRatio={fluid ? "none" : undefined}
      className={fluid ? "block w-full" : "inline-block"}
    >
      <defs>
        <linearGradient id={gradientId} x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stopColor={color} stopOpacity="0.3" />
          <stop offset="100%" stopColor={color} stopOpacity="0" />
        </linearGradient>
      </defs>
      <path d={areaPath} fill={`url(#${gradientId})`} />
      <polyline
        points={polyline}
        fill="none"
        stroke={color}
        strokeWidth="1.5"
        strokeLinecap="round"
        strokeLinejoin="round"
        vectorEffect={fluid ? "non-scaling-stroke" : undefined}
      />
    </svg>
  );
}
