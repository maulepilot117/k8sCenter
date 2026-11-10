/**
 * The axes and tooltip every axis chart uses, so time and value labels read
 * the same everywhere. Each is placed directly inside a Recharts chart.
 */
import { Tooltip, usePlotArea, XAxis, YAxis } from "recharts";
import {
  AXIS_TICK_STYLE,
  CHART_TOKENS,
  formatValue,
  maxTickCount,
  TOOLTIP_PROPS,
  timeTickFormatter,
  timeTicks,
  tooltipTimeFormatter,
  type ValueUnit,
  valueDomain,
} from "./chart-theme.ts";

interface TimeXAxisProps {
  /** Row field holding the timestamp, in epoch milliseconds. */
  dataKey: string;
  /** Range the axis spans, in epoch milliseconds. */
  start: number;
  end: number;
}

/** A time axis over [start, end], with as many ticks as the plot width fits. */
export function TimeXAxis({ dataKey, start, end }: TimeXAxisProps) {
  const plotWidth = usePlotArea()?.width ?? 0;
  return (
    <XAxis
      dataKey={dataKey}
      type="number"
      scale="time"
      domain={[start, end]}
      allowDataOverflow
      ticks={timeTicks(start, end, maxTickCount(plotWidth))}
      interval={0}
      tickFormatter={timeTickFormatter(end - start)}
      tick={AXIS_TICK_STYLE}
      tickLine={false}
      stroke={CHART_TOKENS.grid}
    />
  );
}

interface ValueYAxisProps {
  unit: ValueUnit;
  /** The plotted values, so a constant series gets a padded domain. */
  values: readonly number[];
}

export function ValueYAxis({ unit, values }: ValueYAxisProps) {
  return (
    <YAxis
      domain={valueDomain(values)}
      tickFormatter={(v: number) => formatValue(v, unit)}
      tick={AXIS_TICK_STYLE}
      tickLine={false}
      axisLine={false}
      // Wide enough for a value and its unit ("386.2 MiB", "0.56 cores") on
      // one line; Recharts wraps a tick label that does not fit.
      width={76}
    />
  );
}

interface ChartTooltipProps {
  unit: ValueUnit;
  /** Span of the time axis, which decides whether the label shows the date. */
  rangeMs: number;
}

/** Tooltip with the point's time and each series' value in the chart's unit. */
export function ChartTooltip({ unit, rangeMs }: ChartTooltipProps) {
  const timeLabel = tooltipTimeFormatter(rangeMs);
  return (
    <Tooltip
      {...TOOLTIP_PROPS}
      labelFormatter={(t) => timeLabel(Number(t))}
      formatter={(v) => formatValue(Number(v), unit)}
    />
  );
}
