/**
 * The look and axis conventions every Recharts chart shares.
 *
 * Colours are theme-token `var(--…)` strings, never hex values, so charts
 * follow the Liquid Glass theme. Axis charts take their tick count, time
 * labels, value labels and value domain from the functions here, so a time
 * axis reads the same on every page.
 */
import type { CSSProperties } from "react";

export const CHART_TOKENS = {
  line: "var(--accent)",
  grid: "var(--border-subtle)",
  axis: "var(--text-muted)",
  track: "var(--bg-elevated)",
} as const;

/**
 * Props for a chart with no axes (sparklines, gauges, donuts). It fills its
 * ChartFrame and stays out of the tab order: the frame carries the chart's
 * role and label, so the chart is one image to assistive tech.
 */
export const BARE_CHART_PROPS = {
  responsive: true,
  accessibilityLayer: false,
  style: { width: "100%", height: "100%" },
} as const;

/**
 * Props for a chart with axes. Recharts' keyboard layer stays on: the chart
 * is a tab stop, and arrow keys move the tooltip between points.
 */
export const AXIS_CHART_PROPS = {
  responsive: true,
  accessibilityLayer: true,
  style: { width: "100%", height: "100%" },
  margin: { top: 8, right: 12, bottom: 0, left: 0 },
} as const;

export const AXIS_TICK_STYLE = {
  fill: CHART_TOKENS.axis,
  fontSize: 11,
} as const;

const TOOLTIP_CONTENT_STYLE: CSSProperties = {
  background: "var(--bg-elevated)",
  border: "1px solid var(--border-subtle)",
  borderRadius: "8px",
  color: "var(--text-primary)",
  fontSize: "12px",
  padding: "6px 10px",
};

/**
 * Tooltip styling. Recharts opens the tooltip on hover, on touch, and, with
 * the keyboard layer on, when the chart takes focus.
 */
export const TOOLTIP_PROPS = {
  isAnimationActive: false,
  contentStyle: TOOLTIP_CONTENT_STYLE,
  labelStyle: { color: "var(--text-secondary)", marginBottom: "2px" },
  itemStyle: { color: "var(--text-primary)", padding: 0 },
  cursor: { stroke: CHART_TOKENS.grid },
} as const;

/** Narrowest horizontal space one time label needs. */
const MIN_TICK_SPACING_PX = 80;

/** How many x-axis ticks fit in a chart this wide. Always at least the two ends. */
export function maxTickCount(widthPx: number): number {
  return Math.max(2, Math.floor(widthPx / MIN_TICK_SPACING_PX));
}

export const MINUTE = 60_000;
export const HOUR = 60 * MINUTE;
const DAY = 24 * HOUR;

const MINUTE_STEPS = [1, 2, 5, 10, 15, 30].map((m) => m * MINUTE);
const HOUR_STEPS = [1, 2, 3, 6, 12].map((h) => h * HOUR);
const DAY_STEPS = [1, 2, 7, 14, 30].map((d) => d * DAY);

/** Candidate tick steps for a range: minutes up to 1h, hours up to 24h, then days. */
function stepsFor(rangeMs: number): number[] {
  if (rangeMs <= HOUR) return [...MINUTE_STEPS, HOUR];
  if (rangeMs <= DAY) return [...HOUR_STEPS, DAY];
  return DAY_STEPS;
}

/** First local-time multiple of `step` at or after `t`. */
function alignUp(t: number, step: number): number {
  const offset = new Date(t).getTimezoneOffset() * MINUTE;
  return Math.ceil((t - offset) / step) * step + offset;
}

/**
 * Tick timestamps for the time range [start, end]: evenly spaced, on whole
 * local minutes, hours or days, and no more than `maxCount` of them.
 */
export function timeTicks(
  start: number,
  end: number,
  maxCount: number,
): number[] {
  if (!(end > start)) return [];
  const steps = stepsFor(end - start);
  let chosen = steps[steps.length - 1];
  let first = alignUp(start, chosen);
  for (const step of steps) {
    const f = alignUp(start, step);
    if (Math.floor((end - f) / step) + 1 <= maxCount) {
      chosen = step;
      first = f;
      break;
    }
  }
  const ticks: number[] = [];
  for (let t = first; t <= end && ticks.length < maxCount; ) {
    ticks.push(t);
    if (chosen >= DAY) {
      // Calendar days, so a daylight-saving change keeps ticks on midnight.
      const next = new Date(t);
      next.setDate(next.getDate() + chosen / DAY);
      t = next.getTime();
    } else {
      t += chosen;
    }
  }
  return ticks;
}

const MONTHS = [
  "Jan",
  "Feb",
  "Mar",
  "Apr",
  "May",
  "Jun",
  "Jul",
  "Aug",
  "Sep",
  "Oct",
  "Nov",
  "Dec",
];

const pad2 = (n: number) => String(n).padStart(2, "0");

/**
 * The tick label format for a time range: hours and minutes up to 1h, the
 * hour up to 24h, the date beyond that. Pairs with timeTicks, whose ticks
 * fall on whole minutes, hours or days to match.
 */
export function timeTickFormatter(rangeMs: number): (t: number) => string {
  if (rangeMs <= HOUR) {
    return (t) => {
      const d = new Date(t);
      return `${pad2(d.getHours())}:${pad2(d.getMinutes())}`;
    };
  }
  if (rangeMs <= DAY) {
    return (t) => `${pad2(new Date(t).getHours())}:00`;
  }
  return (t) => {
    const d = new Date(t);
    return `${MONTHS[d.getMonth()]} ${d.getDate()}`;
  };
}

/** Tooltip time label: always to the minute, with the date for ranges over a day. */
export function tooltipTimeFormatter(rangeMs: number): (t: number) => string {
  return (t) => {
    const d = new Date(t);
    const time = `${pad2(d.getHours())}:${pad2(d.getMinutes())}`;
    return rangeMs > DAY
      ? `${MONTHS[d.getMonth()]} ${d.getDate()} ${time}`
      : time;
  };
}

export type ValueUnit = "percent" | "bytes" | "cores" | "number";

/** Drops trailing zeros: 1.50 → "1.5", 2.0 → "2". */
function trim(v: number, decimals: number): string {
  return String(Number(v.toFixed(decimals)));
}

const BYTE_UNITS = ["B", "KiB", "MiB", "GiB", "TiB", "PiB"];

export function formatValue(v: number, unit: ValueUnit): string {
  switch (unit) {
    case "percent":
      return `${trim(v, 1)}%`;
    case "cores":
      return `${trim(v, Math.abs(v) < 1 ? 3 : 2)} ${v === 1 ? "core" : "cores"}`;
    case "bytes": {
      let i = 0;
      let n = v;
      while (Math.abs(n) >= 1024 && i < BYTE_UNITS.length - 1) {
        n /= 1024;
        i++;
      }
      return `${trim(n, 1)} ${BYTE_UNITS[i]}`;
    }
    default:
      return trim(v, 2);
  }
}

export type ValueDomain = [number, number] | ["auto", "auto"];

/**
 * The value-axis domain for a series. A constant series would give a
 * zero-height axis, so it is padded around its value, never below zero for a
 * non-negative series. Anything else is left to Recharts' own nice scale.
 */
export function valueDomain(values: readonly number[]): ValueDomain {
  const finite = values.filter((v) => Number.isFinite(v));
  if (finite.length === 0) return ["auto", "auto"];
  const min = Math.min(...finite);
  const max = Math.max(...finite);
  if (min !== max) return ["auto", "auto"];
  const pad = Math.abs(min) * 0.1 || 1;
  return [min >= 0 ? Math.max(0, min - pad) : min - pad, max + pad];
}
