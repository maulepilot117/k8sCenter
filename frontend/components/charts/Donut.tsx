import type { ReactNode } from "react";
import { Pie, PieChart } from "recharts";
import ChartFrame from "./ChartFrame.tsx";
import { BARE_CHART_PROPS } from "./chart-theme.ts";

export interface DonutSegment {
  value: number;
  color: string;
  /** Names the segment in the donut's accessible label. */
  label?: string;
  /** Fills the ring when there is nothing to plot: named without its value. */
  placeholder?: boolean;
}

interface DonutProps {
  segments: DonutSegment[];
  size?: number;
  thickness?: number;
  /** content rendered in the hole (count + caption) */
  center?: ReactNode;
}

/** The label names every labelled segment with its value, drawn or not. */
function donutLabel(segments: DonutSegment[]): string {
  const named = segments.filter((s) => s.label);
  if (named.length === 0) return "No data";
  return named
    .map((s) => (s.placeholder ? s.label : `${s.label}: ${s.value}`))
    .join(", ");
}

/** Status donut: a ring of coloured sectors with `center` in the hole. Pod/workload status. */
export default function Donut({
  segments,
  size = 108,
  thickness = 15,
  center,
}: DonutProps) {
  const rows = segments
    .filter((s) => s.value > 0)
    .map((s) => ({ name: s.label ?? "", value: s.value, fill: s.color }));

  return (
    <ChartFrame
      preset="tile"
      kind="donut"
      label={donutLabel(segments)}
      width={size}
      height={size}
    >
      <PieChart {...BARE_CHART_PROPS}>
        <Pie
          data={rows}
          dataKey="value"
          nameKey="name"
          innerRadius={Math.max(0, size / 2 - thickness)}
          outerRadius={size / 2}
          startAngle={90}
          endAngle={-270}
          stroke="none"
          isAnimationActive={false}
        />
      </PieChart>
      <div
        style={{
          position: "absolute",
          inset: `${thickness}px`,
          display: "flex",
          flexDirection: "column",
          alignItems: "center",
          justifyContent: "center",
        }}
      >
        {center}
      </div>
    </ChartFrame>
  );
}
