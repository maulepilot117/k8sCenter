import type { ReactNode } from "react";

/**
 * `tile`: a bare chart in a dashboard tile (sparkline, gauge, donut), exposed
 * to assistive tech as one image. `axis`: a chart with time and value axes
 * and a keyboard-reachable tooltip, tall enough for two axes and tick labels.
 */
export type ChartPreset = "tile" | "axis";

const PRESET_HEIGHT: Record<ChartPreset, number> = { tile: 28, axis: 200 };

interface ChartFrameProps {
  preset: ChartPreset;
  /** Accessible name. A tile chart's label should state the value it shows. */
  label: string;
  /** What the chart is, e.g. "sparkline" or "gauge"; read by tests and e2e. */
  kind: string;
  /** Overrides the preset height, in px. */
  height?: number;
  /** Fixed width in px, for square charts. Omit to fill the container. */
  width?: number;
  /** Nothing to draw: shows `emptyMessage` in the box instead of the chart. */
  empty?: boolean;
  emptyMessage?: ReactNode;
  /** The Recharts chart, plus anything overlaid on it such as a gauge's centre value. */
  children: ReactNode;
}

/**
 * The fixed-size box every chart draws into.
 *
 * Recharts measures its container in the browser and draws nothing on the
 * server, so the box holds the chart's space from the first server render and
 * the page does not shift when the chart appears. Tests and e2e read the
 * `data-chart-*` attributes and the label, never Recharts' own SVG.
 */
export default function ChartFrame({
  preset,
  label,
  kind,
  height = PRESET_HEIGHT[preset],
  width,
  empty = false,
  emptyMessage = "No data",
  children,
}: ChartFrameProps) {
  const box = {
    position: "relative",
    width: width === undefined ? "100%" : `${width}px`,
    height: `${height}px`,
    flexShrink: width === undefined ? undefined : 0,
  } as const;
  const frameAttrs = {
    "data-chart-frame": "",
    "data-chart": kind,
    "data-chart-preset": preset,
  };

  if (empty) {
    return (
      <div
        {...frameAttrs}
        data-chart-state="empty"
        className="flex items-center justify-center text-xs"
        style={{ ...box, color: "var(--text-muted)" }}
      >
        {emptyMessage}
      </div>
    );
  }

  return (
    <div
      {...frameAttrs}
      data-chart-state="ready"
      role={preset === "tile" ? "img" : "figure"}
      aria-label={label}
      style={box}
    >
      {children}
    </div>
  );
}
