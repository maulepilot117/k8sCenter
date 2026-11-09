/** @jsxImportSource react */
import { useEffect, useId, useState } from "react";

interface GaugeRingProps {
  value: number; // 0-100
  size?: number;
  strokeWidth?: number;
  color: string;
  secondaryColor?: string;
  label?: string;
  displayValue?: string; // Override the default "{value}%" display
  valueSize?: string; // e.g., "42px" — override font size
  valueGradient?: boolean; // Use gradient text
  // When true, renders the arc fully filled in `color` regardless of `value`
  // and skips the gradient (used for the "unknown" health status — a null score
  // would otherwise draw an empty arc).
  indeterminate?: boolean;
}

export function GaugeRing({
  value,
  size = 80,
  strokeWidth = 6,
  color,
  secondaryColor,
  label,
  displayValue,
  valueSize,
  valueGradient,
  indeterminate = false,
}: GaugeRingProps) {
  const reactId = useId();
  const gradientId =
    !indeterminate && secondaryColor ? `gauge-${reactId}` : null;

  const radius = (size - strokeWidth) / 2;
  const circumference = 2 * Math.PI * radius;
  const clampedValue = Math.max(0, Math.min(100, value));
  // When indeterminate, the arc is fully filled (offset = 0).
  const offset = indeterminate
    ? 0
    : circumference - (clampedValue / 100) * circumference;
  const center = size / 2;

  // Animate from fully hidden to target offset on mount / value change
  const [mountedOffset, setMountedOffset] = useState(circumference);

  useEffect(() => {
    // Small delay to ensure the initial state renders first
    const id = setTimeout(() => {
      setMountedOffset(offset);
    }, 50);
    return () => clearTimeout(id);
  }, [offset]);

  return (
    <div
      className="relative inline-flex items-center justify-center"
      style={{ width: `${size}px`, height: `${size}px` }}
    >
      <svg width={size} height={size} viewBox={`0 0 ${size} ${size}`}>
        {secondaryColor && gradientId && (
          <defs>
            <linearGradient id={gradientId} x1="0" y1="0" x2="1" y2="1">
              <stop offset="0%" stopColor={color} />
              <stop offset="100%" stopColor={secondaryColor} />
            </linearGradient>
          </defs>
        )}
        {/* Track */}
        <circle
          cx={center}
          cy={center}
          r={radius}
          fill="none"
          stroke="var(--bg-elevated)"
          strokeWidth={strokeWidth}
        />
        {/* Progress */}
        <circle
          cx={center}
          cy={center}
          r={radius}
          fill="none"
          stroke={!indeterminate && gradientId ? `url(#${gradientId})` : color}
          strokeWidth={strokeWidth}
          strokeLinecap="round"
          strokeDasharray={circumference}
          strokeDashoffset={mountedOffset}
          transform={`rotate(-90 ${center} ${center})`}
          style={{
            transition: "stroke-dashoffset 1s cubic-bezier(0.16, 1, 0.3, 1)",
          }}
        />
      </svg>
      <div className="absolute inset-0 flex flex-col items-center justify-center">
        <span
          style={{
            fontSize: valueSize ?? "14px",
            fontWeight: valueSize ? 700 : 600,
            fontFamily: "var(--font-mono, monospace)",
            ...(valueGradient
              ? {
                  background:
                    "linear-gradient(135deg, var(--accent), var(--accent-secondary))",
                  WebkitBackgroundClip: "text",
                  WebkitTextFillColor: "transparent",
                }
              : { color: "var(--text-primary)" }),
          }}
        >
          {displayValue ?? `${Math.round(clampedValue)}%`}
        </span>
        {label && (
          <span className="text-xs" style={{ color: "var(--text-muted)" }}>
            {label}
          </span>
        )}
      </div>
    </div>
  );
}
