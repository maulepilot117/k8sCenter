import { useId, useMemo } from "react";
import { Area, AreaChart } from "recharts";
import type { TrendSeries } from "@/lib/dashboard/wire-types.ts";
import { isFiniteNumber } from "@/lib/format.ts";
import { ChartTooltip, TimeXAxis, ValueYAxis } from "./ChartAxes.tsx";
import ChartFrame from "./ChartFrame.tsx";
import { AXIS_CHART_PROPS, HOUR, MINUTE } from "./chart-theme.ts";

// The one-hour tab: what the backend uses when it is not told a range.
const DEFAULT_GRID = { windowMs: HOUR, stepMs: 2 * MINUTE };

// A minimum-size dashboard card (4 rows of 40px, 3 gaps of 16px) leaves about
// 132px for the body once padding and the title row are taken out. The axis
// preset's 200px would overflow and scroll it.
const CHART_HEIGHT = 128;

const EMPTY_MESSAGE = "Trend data unavailable (Prometheus not connected)";

function parseGoDuration(value: string | undefined): number | null {
  if (!value) return null;
  const match = /^(?:(\d+)h)?(?:(\d+)m)?(?:(\d+(?:\.\d+)?)s)?$/.exec(value);
  if (!match || !(match[1] || match[2] || match[3])) return null;
  const ms =
    Number(match[1] ?? 0) * HOUR +
    Number(match[2] ?? 0) * MINUTE +
    Number(match[3] ?? 0) * 1000;
  return ms > 0 ? ms : null;
}

/**
 * The window and sample spacing from the response's Go durations. Both must
 * parse, else both take the default: half a grid would put the samples at the
 * wrong spacing.
 */
function resolveGrid(
  span: string | undefined,
  step: string | undefined,
): { windowMs: number; stepMs: number } {
  const windowMs = parseGoDuration(span);
  const stepMs = parseGoDuration(step);
  return windowMs && stepMs ? { windowMs, stepMs } : DEFAULT_GRID;
}

/** "hour", "15 minutes", "6 hours": the window as the label names it. */
function describeWindow(ms: number): string {
  if (ms === HOUR) return "hour";
  if (ms > HOUR && ms % HOUR === 0) return `${ms / HOUR} hours`;
  const minutes = Math.round(ms / MINUTE);
  return minutes === 1 ? "minute" : `${minutes} minutes`;
}

interface TrendRow {
  t: number;
  cpu?: number;
  memory?: number;
}

/**
 * One row per sample time. The backend puts every series on one grid that
 * ends at `end`, with null for a step Prometheus had no value for, so point i
 * of n sits at end - (n - 1 - i) * step. A null or non-finite entry is skipped
 * but keeps its index: the samples before it stay on their own steps, and CPU
 * and memory line up step for step.
 *
 * A step both series lost gets no row, while a step only one lost gets a row
 * without that key. The areas set `connectNulls` so the two cases draw alike:
 * one line over the finite samples, the way the sparklines draw the same
 * series. The tooltip still offers only the steps that hold a sample.
 */
function buildRows(
  cpu: TrendSeries,
  memory: TrendSeries,
  end: number,
  step: number,
): TrendRow[] {
  const rows = new Map<number, TrendRow>();
  const place = (values: TrendSeries, key: "cpu" | "memory") => {
    values.forEach((v, i) => {
      if (!isFiniteNumber(v)) return;
      const t = end - (values.length - 1 - i) * step;
      const row = rows.get(t) ?? { t };
      row[key] = v;
      rows.set(t, row);
    });
  };
  place(cpu, "cpu");
  place(memory, "memory");
  return [...rows.values()].sort((a, b) => a.t - b.t);
}

export interface ResourceAreaChartProps {
  /** CPU percentages on the response's grid; null entries are gaps. */
  cpuData: TrendSeries | null;
  /** Memory percentages on the same grid as `cpuData`. */
  memData: TrendSeries | null;
  /** Span of the series as a Go duration ("1h0m0s"). Defaults to one hour. */
  window?: string;
  /** Spacing between samples as a Go duration ("2m0s"). Defaults to two minutes. */
  step?: string;
  /** When the newest sample was taken, in epoch milliseconds. */
  end: number;
}

/** CPU and memory utilisation as two filled areas on a shared time axis. */
export function ResourceAreaChart({
  cpuData,
  memData,
  window: span,
  step,
  end,
}: ResourceAreaChartProps) {
  const id = useId();
  const cpuGradId = `cpu-grad-${id}`;
  const memGradId = `mem-grad-${id}`;

  const { windowMs, stepMs } = resolveGrid(span, step);
  const { rows, values, hasCpu, hasMemory } = useMemo(() => {
    const rows = buildRows(cpuData ?? [], memData ?? [], end, stepMs);
    return {
      rows,
      values: rows.flatMap((r) => [r.cpu, r.memory]).filter(isNumber),
      hasCpu: rows.some((r) => r.cpu !== undefined),
      hasMemory: rows.some((r) => r.memory !== undefined),
    };
  }, [cpuData, memData, end, stepMs]);

  return (
    <ChartFrame
      preset="axis"
      kind="resource-area"
      label={`CPU and memory utilization over the last ${describeWindow(windowMs)}`}
      height={CHART_HEIGHT}
      empty={rows.length === 0}
      emptyMessage={EMPTY_MESSAGE}
    >
      <AreaChart {...AXIS_CHART_PROPS} data={rows}>
        <defs>
          <linearGradient id={cpuGradId} x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor="var(--accent)" stopOpacity={0.35} />
            <stop offset="100%" stopColor="var(--accent)" stopOpacity={0.02} />
          </linearGradient>
          <linearGradient id={memGradId} x1="0" y1="0" x2="0" y2="1">
            <stop
              offset="0%"
              stopColor="var(--accent-secondary)"
              stopOpacity={0.3}
            />
            <stop
              offset="100%"
              stopColor="var(--accent-secondary)"
              stopOpacity={0.02}
            />
          </linearGradient>
        </defs>
        <TimeXAxis dataKey="t" start={end - windowMs} end={end} />
        <ValueYAxis unit="percent" values={values} />
        {hasMemory && (
          <Area
            dataKey="memory"
            name="Memory"
            type="monotone"
            stroke="var(--accent-secondary)"
            strokeWidth={1.8}
            fill={`url(#${memGradId})`}
            connectNulls
            dot={false}
            isAnimationActive={false}
          />
        )}
        {hasCpu && (
          <Area
            dataKey="cpu"
            name="CPU"
            type="monotone"
            stroke="var(--accent)"
            strokeWidth={1.8}
            fill={`url(#${cpuGradId})`}
            connectNulls
            dot={false}
            isAnimationActive={false}
          />
        )}
        <ChartTooltip unit="percent" rangeMs={windowMs} />
      </AreaChart>
    </ChartFrame>
  );
}

function isNumber(v: number | undefined): v is number {
  return v !== undefined;
}
