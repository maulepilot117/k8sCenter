import { type ReactNode, useId } from "react";
import { PolarAngleAxis, RadialBar, RadialBarChart } from "recharts";
import ChartFrame from "./ChartFrame.tsx";
import { BARE_CHART_PROPS } from "./chart-theme.ts";

interface RadialGaugeProps {
  /** 0..100; values outside are clamped. */
  percent: number;
  /** Side of the square frame, in px. */
  size: number;
  /** Ring thickness, in px. */
  thickness: number;
  color: string;
  /** Fades the ring from `color` to this colour, corner to corner. */
  secondaryColor?: string;
  track: string;
  /** Accessible name; states the value the ring shows. */
  label: string;
  /** Centre content, laid over the ring. */
  children?: ReactNode;
}

/**
 * A circular progress ring: one round-capped bar over a full-circle track,
 * starting at twelve o'clock and running clockwise. Cluster health, policy
 * compliance and ESO sync gauges all draw through this one component.
 */
export default function RadialGauge({
  percent,
  size,
  thickness,
  color,
  secondaryColor,
  track,
  label,
  children,
}: RadialGaugeProps) {
  const gradientId = `radial-gauge-${useId().replace(/:/g, "")}`;
  const clamped = Math.max(0, Math.min(100, percent));
  const outerRadius = size / 2 - 1;
  const fill = secondaryColor ? `url(#${gradientId})` : color;

  return (
    <ChartFrame
      preset="tile"
      kind="gauge"
      label={label}
      width={size}
      height={size}
    >
      <RadialBarChart
        {...BARE_CHART_PROPS}
        data={[{ value: clamped, fill }]}
        innerRadius={outerRadius - thickness}
        outerRadius={outerRadius}
        startAngle={90}
        endAngle={-270}
        margin={{ top: 0, right: 0, bottom: 0, left: 0 }}
      >
        {secondaryColor && (
          <defs>
            <linearGradient id={gradientId} x1="0" y1="0" x2="1" y2="1">
              <stop offset="0%" stopColor={color} />
              <stop offset="100%" stopColor={secondaryColor} />
            </linearGradient>
          </defs>
        )}
        <PolarAngleAxis type="number" domain={[0, 100]} tick={false} />
        <RadialBar
          dataKey="value"
          background={{ fill: track }}
          cornerRadius={thickness / 2}
          isAnimationActive={false}
        />
      </RadialBarChart>
      {children && (
        <div className="absolute inset-0 flex flex-col items-center justify-center">
          {children}
        </div>
      )}
    </ChartFrame>
  );
}
