import { useId } from "react";
import { Area, AreaChart, YAxis } from "recharts";
import ChartFrame from "@/components/charts/ChartFrame.tsx";
import {
  BARE_CHART_PROPS,
  CHART_TOKENS,
  formatValue,
  valueDomain,
} from "@/components/charts/chart-theme.ts";

interface SparklineProps {
  data: number[];
  /** What the series is, read out with its latest value, e.g. "CPU usage". */
  name?: string;
  /** Fixed pixel width. Omit to fill the container (default). */
  width?: number;
  height?: number;
  stroke?: string;
  /** area fill below the line (e.g. "color-mix(in srgb, var(--accent) 16%, transparent)") */
  fill?: string;
  /** Fill below the line with a gradient fading out from the stroke colour. */
  gradient?: boolean;
  strokeWidth?: number;
  /** true (default) = min/max scale to fit; false = treat values as 0..100 */
  normalize?: boolean;
}

const CHART_MARGIN = { top: 2, right: 0, bottom: 2, left: 0 };

/** Single-series sparkline: a bare Recharts line in a tile frame. */
export default function Sparkline({
  data,
  name = "Trend",
  width,
  height = 34,
  stroke = CHART_TOKENS.line,
  fill,
  gradient = false,
  strokeWidth = 1.8,
  normalize = true,
}: SparklineProps) {
  const gradientId = `spark-${useId()}`;

  const clean = data.filter((v) => Number.isFinite(v));
  const latest = clean[clean.length - 1];
  const rows = (clean.length === 1 ? [clean[0], clean[0]] : clean).map((v) => ({
    v,
  }));
  const range = valueDomain(clean);
  const domain: [number | string, number | string] = !normalize
    ? [0, 100]
    : range[0] === "auto"
      ? ["dataMin", "dataMax"]
      : range;

  return (
    <ChartFrame
      preset="tile"
      kind="sparkline"
      label={
        latest === undefined
          ? name
          : `${name}, latest ${formatValue(latest, "number")}`
      }
      height={height}
      width={width}
      empty={clean.length === 0}
      emptyMessage={null}
    >
      <AreaChart {...BARE_CHART_PROPS} data={rows} margin={CHART_MARGIN}>
        {gradient && (
          <defs>
            <linearGradient id={gradientId} x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor={stroke} stopOpacity={0.3} />
              <stop offset="100%" stopColor={stroke} stopOpacity={0} />
            </linearGradient>
          </defs>
        )}
        <YAxis hide domain={domain} />
        <Area
          dataKey="v"
          type="linear"
          stroke={stroke}
          strokeWidth={strokeWidth}
          fill={gradient ? `url(#${gradientId})` : (fill ?? "none")}
          isAnimationActive={false}
          dot={false}
        />
      </AreaChart>
    </ChartFrame>
  );
}
