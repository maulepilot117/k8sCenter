import { GaugeRing } from "@/components/ui/GaugeRing.tsx";

interface UtilizationGaugeProps {
  title: string;
  value: number;
  used: string;
  total: string;
  requests?: string;
  limits?: string;
  color: string;
  secondaryColor?: string;
  trendColor?: string;
  /**
   * Historical utilization percentages (oldest→newest) for the trend line.
   * Sourced from /v1/cluster/dashboard-trends. When absent or shorter than two
   * points (Prometheus unavailable), no trend line renders.
   */
  trendData?: number[];
}

// Trend SVG coordinate space — fixed viewBox stretched to the card width via
// preserveAspectRatio="none". 3px vertical padding keeps the line off the edges.
const TREND_W = 300;
const TREND_H = 48;

// buildTrendPaths maps a utilization series to a line path and a filled-area
// path. The series is min/max-normalized so recent movement is visible even
// when utilization hovers in a narrow band (the gauge ring shows the absolute
// level; this line shows the shape of the last hour).
function buildTrendPaths(data: number[]): { line: string; area: string } {
  const min = Math.min(...data);
  const max = Math.max(...data);
  const range = max - min || 1;
  const pts = data.map((v, i) => {
    const x = (i / (data.length - 1)) * TREND_W;
    const y = TREND_H - ((v - min) / range) * (TREND_H - 6) - 3;
    return `${x.toFixed(1)},${y.toFixed(1)}`;
  });
  const line = `M${pts.join(" L")}`;
  const area = `${line} L${TREND_W},${TREND_H} L0,${TREND_H} Z`;
  return { line, area };
}

export default function UtilizationGauge({
  title,
  value,
  used,
  total,
  requests,
  limits,
  color,
  secondaryColor,
  trendColor,
  trendData,
}: UtilizationGaugeProps) {
  const trendStroke = trendColor ?? color;
  const gradientId = `trend-${Math.random().toString(36).slice(2, 9)}`;
  // Drop non-finite samples (NaN/±Infinity) before building the path — they
  // would yield broken `M x,NaN` coordinates and an invisible, silent chart.
  const trendClean = trendData?.filter((v) => Number.isFinite(v));
  const trend = trendClean && trendClean.length >= 2
    ? buildTrendPaths(trendClean)
    : null;
  const statRows: { label: string; value: string }[] = [
    { label: "Used", value: `${used} / ${total}` },
  ];
  if (requests) statRows.push({ label: "Requests", value: requests });
  if (limits) statRows.push({ label: "Limits", value: limits });

  return (
    <div
      style={{
        background: "var(--bg-surface)",
        border: "1px solid var(--border-primary)",
        borderRadius: "var(--radius)",
        padding: "20px",
        transition: "border-color 0.2s ease",
      }}
    >
      {/* Title — matches .card-title from mockup */}
      <div
        style={{
          fontSize: "11px",
          fontWeight: 600,
          textTransform: "uppercase",
          letterSpacing: "0.08em",
          color: "var(--text-muted)",
          marginBottom: "16px",
        }}
      >
        {title}
      </div>

      {/* Gauge + stats row — matches .util-content */}
      <div
        style={{
          display: "flex",
          alignItems: "center",
          gap: "24px",
        }}
      >
        {/* Gauge — matches .gauge-container (100x100, flex-shrink: 0) */}
        <div style={{ flexShrink: 0 }}>
          <GaugeRing
            value={value}
            size={100}
            strokeWidth={10}
            color={color}
            secondaryColor={secondaryColor}
            displayValue={`${Math.round(value)}%`}
            valueSize="22px"
          />
        </div>

        {/* Stats table — matches .util-details */}
        <div style={{ flex: 1 }}>
          {statRows.map((row, idx) => (
            <div
              key={row.label}
              style={{
                display: "flex",
                justifyContent: "space-between",
                alignItems: "center",
                padding: "6px 0",
                fontSize: "13px",
                borderBottom: idx < statRows.length - 1
                  ? "1px solid var(--border-subtle)"
                  : "none",
              }}
            >
              <span style={{ color: "var(--text-secondary)" }}>
                {row.label}
              </span>
              <span
                style={{
                  fontFamily: "var(--font-mono, monospace)",
                  fontWeight: 500,
                  color: "var(--text-primary)",
                }}
              >
                {row.value}
              </span>
            </div>
          ))}
        </div>
      </div>

      {
        /* Trend line chart — real last-hour utilization, or nothing if
          Prometheus history is unavailable (no fake decorative line). */
      }
      {trend && (
        <div style={{ marginTop: "16px", height: "48px" }}>
          <svg
            viewBox={`0 0 ${TREND_W} ${TREND_H}`}
            preserveAspectRatio="none"
            width="100%"
            height={TREND_H}
          >
            <defs>
              <linearGradient id={gradientId} x1="0" y1="0" x2="0" y2="1">
                <stop
                  offset="0%"
                  stop-color={trendStroke}
                  stop-opacity="0.15"
                />
                <stop
                  offset="100%"
                  stop-color={trendStroke}
                  stop-opacity="0"
                />
              </linearGradient>
            </defs>
            <path d={trend.area} fill={`url(#${gradientId})`} />
            <path
              d={trend.line}
              fill="none"
              stroke={trendStroke}
              stroke-width="1.5"
              vector-effect="non-scaling-stroke"
            />
          </svg>
        </div>
      )}
    </div>
  );
}
