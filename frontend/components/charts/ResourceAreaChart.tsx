import { useMemo } from "preact/hooks";

// ─── Chart constants ─────────────────────────────────────────────────────────

const CHART_W = 400;
const CHART_H = 80;
const CHART_PAD = 4;

function buildSeriesPath(data: number[]): { line: string; area: string } {
  const n = data.length;
  if (n < 2) return { line: "", area: "" };
  const min = 0; // % data is always 0–100
  const range = 100;
  const x = (i: number) =>
    CHART_PAD + (i * (CHART_W - 2 * CHART_PAD)) / (n - 1);
  const y = (v: number) =>
    CHART_H - CHART_PAD -
    ((v - min) / range) * (CHART_H - 2 * CHART_PAD);
  const pts = data.map((v, i) => `${x(i).toFixed(1)},${y(v).toFixed(1)}`);
  const line = `M${pts.join(" L")}`;
  const area = `${line} L${x(n - 1).toFixed(1)},${
    (CHART_H - CHART_PAD).toFixed(1)
  } L${x(0).toFixed(1)},${(CHART_H - CHART_PAD).toFixed(1)} Z`;
  return { line, area };
}

// ─── Multi-series area chart ──────────────────────────────────────────────────
// Renders two filled area series (CPU + Memory) from trend data.
// Falls back gracefully to a placeholder when data is absent.

export interface ResourceAreaChartProps {
  cpuData: number[] | null;
  memData: number[] | null;
}

export function ResourceAreaChart(
  { cpuData, memData }: ResourceAreaChartProps,
) {
  const cpuPath = useMemo(
    () => cpuData && cpuData.length >= 2 ? buildSeriesPath(cpuData) : null,
    [cpuData],
  );
  const memPath = useMemo(
    () => memData && memData.length >= 2 ? buildSeriesPath(memData) : null,
    [memData],
  );

  const cpuGradId = useMemo(
    () => `cpu-grad-${Math.random().toString(36).slice(2, 9)}`,
    [],
  );
  const memGradId = useMemo(
    () => `mem-grad-${Math.random().toString(36).slice(2, 9)}`,
    [],
  );

  if (!cpuPath && !memPath) {
    return (
      <div
        style={{
          height: `${CHART_H}px`,
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          color: "var(--text-muted)",
          fontSize: "12px",
        }}
      >
        Trend data unavailable (Prometheus not connected)
      </div>
    );
  }

  return (
    <svg
      viewBox={`0 0 ${CHART_W} ${CHART_H}`}
      preserveAspectRatio="none"
      width="100%"
      height={CHART_H}
      style={{ display: "block" }}
    >
      <defs>
        <linearGradient id={cpuGradId} x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stop-color="var(--accent)" stop-opacity="0.35" />
          <stop offset="100%" stop-color="var(--accent)" stop-opacity="0.02" />
        </linearGradient>
        <linearGradient id={memGradId} x1="0" y1="0" x2="0" y2="1">
          <stop
            offset="0%"
            stop-color="var(--accent-secondary)"
            stop-opacity="0.30"
          />
          <stop
            offset="100%"
            stop-color="var(--accent-secondary)"
            stop-opacity="0.02"
          />
        </linearGradient>
      </defs>
      {memPath && (
        <>
          <path d={memPath.area} fill={`url(#${memGradId})`} />
          <path
            d={memPath.line}
            fill="none"
            stroke="var(--accent-secondary)"
            stroke-width="1.8"
            stroke-linecap="round"
            stroke-linejoin="round"
            vector-effect="non-scaling-stroke"
          />
        </>
      )}
      {cpuPath && (
        <>
          <path d={cpuPath.area} fill={`url(#${cpuGradId})`} />
          <path
            d={cpuPath.line}
            fill="none"
            stroke="var(--accent)"
            stroke-width="1.8"
            stroke-linecap="round"
            stroke-linejoin="round"
            vector-effect="non-scaling-stroke"
          />
        </>
      )}
    </svg>
  );
}
