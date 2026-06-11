import { useMemo } from "preact/hooks";

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

export function SparklineChart(
  { data, color, width, height = 28 }: SparklineChartProps,
) {
  // Use Math.random instead of crypto.randomUUID — the latter requires
  // a secure context (HTTPS) and fails on HTTP-only deployments (homelab).
  const gradientId = useMemo(
    () => `spark-${Math.random().toString(36).slice(2, 10)}`,
    [],
  );

  if (data.length < 2) return null;

  // When no explicit pixel width is given, render fluid: the SVG fills its
  // container width and the path coordinates are computed against VIEW_W,
  // then non-uniformly scaled to fit. vector-effect keeps the stroke crisp.
  const fluid = width === undefined;
  const w = fluid ? VIEW_W : width;

  const min = Math.min(...data);
  const max = Math.max(...data);
  const range = max - min || 1;

  const points = data.map((v, i) => {
    const x = (i / (data.length - 1)) * w;
    const y = height - ((v - min) / range) * (height - 2) - 1;
    return `${x},${y}`;
  });

  const polyline = points.join(" ");
  const areaPath = `M0,${height} L${
    points.map((p) => `${p}`).join(" L")
  } L${w},${height} Z`;

  return (
    <svg
      width={fluid ? "100%" : width}
      height={height}
      viewBox={`0 0 ${w} ${height}`}
      preserveAspectRatio={fluid ? "none" : undefined}
      class={fluid ? "block w-full" : "inline-block"}
    >
      <defs>
        <linearGradient id={gradientId} x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stop-color={color} stop-opacity="0.3" />
          <stop offset="100%" stop-color={color} stop-opacity="0" />
        </linearGradient>
      </defs>
      <path d={areaPath} fill={`url(#${gradientId})`} />
      <polyline
        points={polyline}
        fill="none"
        stroke={color}
        stroke-width="1.5"
        stroke-linecap="round"
        stroke-linejoin="round"
        vector-effect={fluid ? "non-scaling-stroke" : undefined}
      />
    </svg>
  );
}
