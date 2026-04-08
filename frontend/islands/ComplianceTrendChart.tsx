import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { useCallback, useEffect } from "preact/hooks";
import { apiGet } from "@/lib/api.ts";
import { Spinner } from "@/components/ui/Spinner.tsx";
import { Button } from "@/components/ui/Button.tsx";

interface TrendPoint {
  date: string;
  score: number;
  pass: number;
  fail: number;
  total: number;
}

interface FilledPoint {
  date: string;
  score: number | null;
  pass: number;
  fail: number;
  total: number;
}

type TimeRange = 7 | 30 | 90;

function scoreColor(score: number): string {
  if (score >= 80) return "var(--success)";
  if (score >= 50) return "var(--warning)";
  return "var(--danger)";
}

function formatDate(dateStr: string): string {
  const d = new Date(dateStr + "T00:00:00");
  return d.toLocaleDateString("en-US", { month: "short", day: "numeric" });
}

function addDays(dateStr: string, days: number): string {
  const d = new Date(dateStr + "T00:00:00");
  d.setDate(d.getDate() + days);
  return d.toISOString().slice(0, 10);
}

function fillDateGaps(points: TrendPoint[]): FilledPoint[] {
  if (points.length === 0) return [];
  const lookup = new Map(points.map((p) => [p.date, p]));
  const result: FilledPoint[] = [];
  const first = points[0].date;
  const last = points[points.length - 1].date;
  let current = first;
  while (current <= last) {
    const existing = lookup.get(current);
    if (existing) {
      result.push({ ...existing });
    } else {
      result.push({ date: current, score: null, pass: 0, fail: 0, total: 0 });
    }
    current = addDays(current, 1);
  }
  return result;
}

function buildSegments(
  points: FilledPoint[],
  width: number,
  height: number,
): { x: number; y: number }[][] {
  const segments: { x: number; y: number }[][] = [];
  let current: { x: number; y: number }[] = [];
  for (let i = 0; i < points.length; i++) {
    const p = points[i];
    if (p.score === null) {
      if (current.length > 0) {
        segments.push(current);
        current = [];
      }
      continue;
    }
    const x = points.length === 1
      ? width / 2
      : (i / (points.length - 1)) * width;
    const y = height - (p.score / 100) * height;
    current.push({ x, y });
  }
  if (current.length > 0) segments.push(current);
  return segments;
}

function TrendChart({
  points,
  hoverIdx,
  onHover,
}: {
  points: FilledPoint[];
  hoverIdx: number | null;
  onHover: (idx: number | null) => void;
}) {
  const W = 600;
  const H = 250;
  const PAD = { top: 10, right: 10, bottom: 30, left: 40 };
  const chartW = W - PAD.left - PAD.right;
  const chartH = H - PAD.top - PAD.bottom;

  const segments = buildSegments(points, chartW, chartH);

  const gridLines = [0, 25, 50, 75, 100];

  const labelCount = Math.min(7, points.length);
  const labelIndices: number[] = [];
  if (points.length > 0) {
    for (let i = 0; i < labelCount; i++) {
      labelIndices.push(
        Math.round((i / (labelCount - 1)) * (points.length - 1)),
      );
    }
  }

  const hoveredPoint = hoverIdx !== null ? points[hoverIdx] : null;
  const hoveredX = hoverIdx !== null && points.length > 1
    ? (hoverIdx / (points.length - 1)) * chartW
    : hoverIdx !== null
    ? chartW / 2
    : null;
  const hoveredY =
    hoveredPoint?.score !== null && hoveredPoint?.score !== undefined
      ? chartH - (hoveredPoint.score / 100) * chartH
      : null;

  function handleMouseMove(e: MouseEvent) {
    const svg = (e.currentTarget as SVGSVGElement).getBoundingClientRect();
    const relX = e.clientX - svg.left - (PAD.left / W) * svg.width;
    const pct = relX / ((chartW / W) * svg.width);
    const idx = Math.round(pct * (points.length - 1));
    if (idx >= 0 && idx < points.length && points[idx].score !== null) {
      onHover(idx);
    } else {
      onHover(null);
    }
  }

  return (
    <svg
      viewBox={`0 0 ${W} ${H}`}
      class="w-full"
      style={{ maxHeight: "250px" }}
      onMouseMove={handleMouseMove}
      onMouseLeave={() => onHover(null)}
    >
      <defs>
        <linearGradient id="trend-grad" x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stop-color="var(--brand)" stop-opacity="0.3" />
          <stop offset="100%" stop-color="var(--brand)" stop-opacity="0" />
        </linearGradient>
      </defs>
      <g transform={`translate(${PAD.left},${PAD.top})`}>
        {gridLines.map((v) => {
          const y = chartH - (v / 100) * chartH;
          return (
            <g key={v}>
              <line
                x1={0}
                y1={y}
                x2={chartW}
                y2={y}
                stroke="var(--border-primary)"
                stroke-width="1"
                stroke-dasharray="4,4"
              />
              <text
                x={-8}
                y={y + 4}
                fill="var(--text-muted)"
                font-size="10"
                text-anchor="end"
              >
                {v}%
              </text>
            </g>
          );
        })}

        {segments.map((seg, si) => {
          if (seg.length < 2) return null;
          const linePath = seg.map((p, i) =>
            `${i === 0 ? "M" : "L"}${p.x},${p.y}`
          ).join(" ");
          const areaPath = linePath +
            ` L${seg[seg.length - 1].x},${chartH} L${seg[0].x},${chartH} Z`;
          return (
            <g key={si}>
              <path d={areaPath} fill="url(#trend-grad)" />
              <polyline
                points={seg.map((p) => `${p.x},${p.y}`).join(" ")}
                fill="none"
                stroke="var(--brand)"
                stroke-width="2"
                stroke-linejoin="round"
              />
            </g>
          );
        })}

        {hoveredX !== null && hoveredY !== null && hoveredPoint && (
          <g>
            <line
              x1={hoveredX}
              y1={0}
              x2={hoveredX}
              y2={chartH}
              stroke="var(--text-muted)"
              stroke-width="1"
              stroke-dasharray="3,3"
            />
            <circle
              cx={hoveredX}
              cy={hoveredY}
              r={4}
              fill="var(--brand)"
              stroke="var(--surface)"
              stroke-width="2"
            />
            <rect
              x={hoveredX - 50}
              y={hoveredY - 32}
              width={100}
              height={24}
              rx={4}
              fill="var(--elevated)"
              stroke="var(--border-primary)"
              stroke-width="1"
            />
            <text
              x={hoveredX}
              y={hoveredY - 16}
              fill="var(--text-primary)"
              font-size="11"
              text-anchor="middle"
            >
              {formatDate(hoveredPoint.date)} &mdash;{" "}
              {hoveredPoint.score!.toFixed(1)}%
            </text>
          </g>
        )}

        {labelIndices.map((idx) => {
          const x = points.length === 1
            ? chartW / 2
            : (idx / (points.length - 1)) * chartW;
          return (
            <text
              key={idx}
              x={x}
              y={chartH + 20}
              fill="var(--text-muted)"
              font-size="10"
              text-anchor="middle"
            >
              {formatDate(points[idx].date)}
            </text>
          );
        })}
      </g>
    </svg>
  );
}

export default function ComplianceTrendChart() {
  const points = useSignal<FilledPoint[]>([]);
  const loading = useSignal(true);
  const error = useSignal<string | null>(null);
  const range = useSignal<TimeRange>(30);
  const hoverIdx = useSignal<number | null>(null);

  const fetchData = useCallback(async (days: TimeRange) => {
    if (!IS_BROWSER) return;
    loading.value = true;
    error.value = null;
    try {
      const res = await apiGet<TrendPoint[]>(
        `/v1/policy/compliance/history?days=${days}`,
      );
      const raw = Array.isArray(res.data) ? res.data : [];
      points.value = fillDateGaps(raw);
    } catch {
      error.value = "Failed to load compliance trend data";
      points.value = [];
    } finally {
      loading.value = false;
    }
  }, []);

  useEffect(() => {
    fetchData(range.value);
  }, [range.value]);

  const realPoints = points.value.filter((p) => p.score !== null);
  const current = realPoints.length > 0
    ? realPoints[realPoints.length - 1].score!
    : null;
  const first = realPoints.length > 0 ? realPoints[0].score! : null;
  const delta = current !== null && first !== null ? current - first : null;
  const best = realPoints.length > 0
    ? Math.max(...realPoints.map((p) => p.score!))
    : null;
  const worst = realPoints.length > 0
    ? Math.min(...realPoints.map((p) => p.score!))
    : null;

  const ranges: TimeRange[] = [7, 30, 90];

  return (
    <div class="rounded-lg border border-border-primary p-6 bg-bg-elevated mb-8">
      <div class="flex items-center justify-between mb-4">
        <h3 class="text-lg font-semibold text-text-primary">
          Compliance Trend
        </h3>
        <div class="flex gap-1">
          {ranges.map((r) => (
            <button
              type="button"
              key={r}
              class={`px-3 py-1 text-xs font-medium rounded border transition-colors ${
                range.value === r
                  ? "bg-brand/10 text-brand border-brand/30"
                  : "text-text-secondary border-border-primary hover:text-text-primary"
              }`}
              onClick={() => {
                range.value = r;
              }}
            >
              {r}d
            </button>
          ))}
        </div>
      </div>

      {loading.value && (
        <div class="flex items-center justify-center py-16">
          <Spinner />
        </div>
      )}

      {!loading.value && error.value && (
        <div class="flex flex-col items-center gap-3 py-12">
          <p class="text-sm text-text-muted">{error.value}</p>
          <Button variant="ghost" onClick={() => fetchData(range.value)}>
            Retry
          </Button>
        </div>
      )}

      {!loading.value && !error.value && realPoints.length === 0 && (
        <p class="text-sm text-text-muted py-12 text-center">
          Compliance trend data will appear after the first snapshot.
        </p>
      )}

      {!loading.value && !error.value && realPoints.length > 0 && (
        <>
          <div class="flex gap-6 mb-4 text-sm">
            <div>
              <span class="text-text-muted">Current:</span>
              <span
                class="font-semibold"
                style={{ color: scoreColor(current!) }}
              >
                {current!.toFixed(1)}
              </span>
            </div>
            {delta !== null && (
              <div>
                <span class="text-text-muted">&#916;</span>
                <span
                  class="font-semibold"
                  style={{
                    color: delta >= 0 ? "var(--success)" : "var(--danger)",
                  }}
                >
                  {delta >= 0 ? "+" : ""}
                  {delta.toFixed(1)}
                </span>
              </div>
            )}
            {best !== null && (
              <div>
                <span class="text-text-muted">Best:</span>
                <span class="font-semibold text-text-primary">
                  {best.toFixed(1)}
                </span>
              </div>
            )}
            {worst !== null && (
              <div>
                <span class="text-text-muted">Worst:</span>
                <span class="font-semibold text-text-primary">
                  {worst.toFixed(1)}
                </span>
              </div>
            )}
          </div>

          <TrendChart
            points={points.value}
            hoverIdx={hoverIdx.value}
            onHover={(idx) => {
              hoverIdx.value = idx;
            }}
          />
        </>
      )}
    </div>
  );
}
