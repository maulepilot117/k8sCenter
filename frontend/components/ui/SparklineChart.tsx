import Sparkline from "@/components/charts/Sparkline.tsx";

interface SparklineChartProps {
  data: number[];
  color: string;
  /** What the series is, read out with its latest value, e.g. "Memory". */
  name?: string;
  /** Fixed pixel width. Omit to fill the parent container (default). */
  width?: number;
  height?: number;
}

/** Sparkline with a gradient fading out below the line. */
export function SparklineChart({
  data,
  color,
  name,
  width,
  height = 28,
}: SparklineChartProps) {
  return (
    <Sparkline
      data={data}
      name={name}
      stroke={color}
      gradient
      strokeWidth={1.5}
      width={width}
      height={height}
    />
  );
}
