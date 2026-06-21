import type { ComponentChildren } from "preact";

export interface DonutSegment {
  value: number;
  color: string;
  label?: string;
}

interface DonutProps {
  segments: DonutSegment[];
  size?: number;
  thickness?: number;
  /** content rendered in the hole (count + caption) */
  center?: ComponentChildren;
}

/** Status donut via conic-gradient (cheap, no SVG arcs). Pod/workload status. */
export default function Donut(
  { segments, size = 108, thickness = 15, center }: DonutProps,
) {
  const total = segments.reduce((a, s) => a + s.value, 0) || 1;
  let acc = 0;
  const stops = segments
    .map((s) => {
      const start = (acc / total) * 100;
      acc += s.value;
      const end = (acc / total) * 100;
      return `${s.color} ${start}% ${end}%`;
    })
    .join(", ");

  return (
    <div
      style={{
        position: "relative",
        width: `${size}px`,
        height: `${size}px`,
        borderRadius: "50%",
        background: `conic-gradient(${stops})`,
        flexShrink: 0,
      }}
    >
      <div
        style={{
          position: "absolute",
          inset: `${thickness}px`,
          borderRadius: "50%",
          background: "var(--bg-surface)",
          display: "flex",
          flexDirection: "column",
          alignItems: "center",
          justifyContent: "center",
        }}
      >
        {center}
      </div>
    </div>
  );
}
