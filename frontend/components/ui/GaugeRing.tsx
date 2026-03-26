import { useMemo } from "preact/hooks";

interface GaugeRingProps {
  value: number; // 0-100
  size?: number;
  strokeWidth?: number;
  color: string;
  secondaryColor?: string;
  label?: string;
}

export function GaugeRing({
  value,
  size = 80,
  strokeWidth = 6,
  color,
  secondaryColor,
  label,
}: GaugeRingProps) {
  // Use Math.random instead of crypto.randomUUID — the latter requires
  // a secure context (HTTPS) and fails on HTTP-only deployments (homelab).
  const gradientId = useMemo(
    () =>
      secondaryColor
        ? `gauge-${Math.random().toString(36).slice(2, 10)}`
        : null,
    [secondaryColor],
  );

  const radius = (size - strokeWidth) / 2;
  const circumference = 2 * Math.PI * radius;
  const clampedValue = Math.max(0, Math.min(100, value));
  const offset = circumference - (clampedValue / 100) * circumference;
  const center = size / 2;

  return (
    <div
      class="relative inline-flex items-center justify-center"
      style={{ width: `${size}px`, height: `${size}px` }}
    >
      <svg width={size} height={size} viewBox={`0 0 ${size} ${size}`}>
        {secondaryColor && gradientId && (
          <defs>
            <linearGradient id={gradientId} x1="0" y1="0" x2="1" y2="1">
              <stop offset="0%" stop-color={color} />
              <stop offset="100%" stop-color={secondaryColor} />
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
          stroke-width={strokeWidth}
        />
        {/* Progress */}
        <circle
          cx={center}
          cy={center}
          r={radius}
          fill="none"
          stroke={gradientId ? `url(#${gradientId})` : color}
          stroke-width={strokeWidth}
          stroke-linecap="round"
          stroke-dasharray={circumference}
          stroke-dashoffset={offset}
          transform={`rotate(-90 ${center} ${center})`}
          style={{ transition: "stroke-dashoffset 0.5s ease" }}
        />
      </svg>
      <div class="absolute inset-0 flex flex-col items-center justify-center">
        <span
          class="text-sm font-semibold"
          style={{ color: "var(--text-primary)" }}
        >
          {clampedValue}%
        </span>
        {label && (
          <span class="text-xs" style={{ color: "var(--text-muted)" }}>
            {label}
          </span>
        )}
      </div>
    </div>
  );
}
