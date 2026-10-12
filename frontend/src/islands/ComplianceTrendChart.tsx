import { useSignal } from "@preact/signals";
import { useCallback, useEffect, useRef } from "preact/hooks";
import { Button } from "@/components/ui/Button.tsx";
import { Spinner } from "@/components/ui/Spinner.tsx";
import { ApiError, apiGet } from "@/lib/api.ts";
import { trendStartIndex } from "@/lib/dashboard/severity.ts";
import { scoreColor } from "@/lib/score-color.ts";
import { selectedCluster } from "@/src/lib/cluster.ts";
import { IS_BROWSER } from "@/src/lib/is-browser.ts";

interface TrendPoint {
  date: string;
  score: number;
  pass: number;
  fail: number;
  total: number;
  /** Remote only: the snapshot rests on less than every policy source. */
  partial?: boolean;
  /** Remote only: the engines the identity could not read. */
  unreadable?: string[];
  /** Remote only: the identity differs from the previous point's. */
  identityChanged?: boolean;
}

/** A day whose snapshot failed, from the response's top-level `gaps` key. */
interface TrendGap {
  date: string;
  reason: string;
}

interface FilledPoint {
  date: string;
  score: number | null;
  pass: number;
  fail: number;
  total: number;
  partial?: boolean;
  unreadable?: string[];
  identityChanged?: boolean;
  /** Set on a day whose snapshot failed; absent on a day with no record. */
  gapReason?: string;
}

type TimeRange = 7 | 30 | 90;

/** One plain sentence per gap reason the recorder writes (KTD4). */
export const GAP_MESSAGES: Readonly<Record<string, string>> = {
  unreachable: "The cluster could not be reached that day.",
  credentials_invalid:
    "The cluster rejected its stored credentials. Re-register the cluster.",
  impersonation_refused:
    "The cluster no longer lets k8sCenter act as this identity. Grant impersonation again.",
  discovery_unavailable: "The cluster's API list could not be read that day.",
  no_policy_engine: "Neither Kyverno nor Gatekeeper is installed.",
  forbidden:
    "The identity could not read any policy source. Widen its read access.",
};

const UNKNOWN_GAP_MESSAGE = "A snapshot could not be taken that day.";

export function gapMessage(reason: string): string {
  return GAP_MESSAGES[reason] ?? UNKNOWN_GAP_MESSAGE;
}

const ENGINE_NAMES: Readonly<Record<string, string>> = {
  kyverno: "Kyverno",
  gatekeeper: "Gatekeeper",
};

function engineLabel(engine: string): string {
  return ENGINE_NAMES[engine.toLowerCase()] ?? engine;
}

function partialNote(p: FilledPoint): string {
  const names = (p.unreadable ?? []).map(engineLabel);
  return names.length > 0
    ? `partial: ${names.join(", ")} could not be read`
    : "partial: not every policy source could be read";
}

function formatDate(dateStr: string): string {
  const d = new Date(`${dateStr}T00:00:00`);
  return d.toLocaleDateString("en-US", { month: "short", day: "numeric" });
}

/**
 * Calendar-day arithmetic in UTC. The old local-midnight build with UTC
 * formatting never advanced east of UTC (local midnight is the previous UTC
 * day), so the fill loop did not terminate there.
 */
export function addDays(dateStr: string, days: number): string {
  const [y, m, d] = dateStr.split("-").map(Number);
  return new Date(Date.UTC(y, m - 1, d + days)).toISOString().slice(0, 10);
}

/**
 * Fills every day from the first record (point or gap) through the end of the
 * window, so a gap after the last point still shows. Days with neither a point
 * nor a gap stay empty (score null, no gapReason).
 */
export function fillDateGaps(
  points: TrendPoint[],
  gaps: TrendGap[],
  windowEnd: string,
): FilledPoint[] {
  const lookup = new Map(points.map((p) => [p.date, p]));
  const gapLookup = new Map(gaps.map((g) => [g.date, g.reason]));
  const dates = [...lookup.keys(), ...gapLookup.keys()].sort();
  if (dates.length === 0) return [];
  const first = dates[0];
  const lastRecord = dates[dates.length - 1];
  const last = lastRecord > windowEnd ? lastRecord : windowEnd;
  const result: FilledPoint[] = [];
  let current = first;
  while (current <= last) {
    const existing = lookup.get(current);
    if (existing) {
      result.push({ ...existing });
    } else {
      const reason = gapLookup.get(current);
      result.push({
        date: current,
        score: null,
        pass: 0,
        fail: 0,
        total: 0,
        ...(reason !== undefined ? { gapReason: reason } : {}),
      });
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
    const x =
      points.length === 1 ? width / 2 : (i / (points.length - 1)) * width;
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
        labelCount === 1
          ? 0
          : Math.round((i / (labelCount - 1)) * (points.length - 1)),
      );
    }
  }

  const xAt = (i: number) =>
    points.length === 1 ? chartW / 2 : (i / (points.length - 1)) * chartW;

  const hoveredPoint = hoverIdx !== null ? points[hoverIdx] : null;
  const hoveredX = hoverIdx !== null ? xAt(hoverIdx) : null;
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
          const linePath = seg
            .map((p, i) => `${i === 0 ? "M" : "L"}${p.x},${p.y}`)
            .join(" ");
          const areaPath =
            linePath +
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

        {points.map((p, i) =>
          p.identityChanged && p.score !== null ? (
            <g key={`id-${p.date}`}>
              <line
                data-testid={`compliance-trend-identity-change-${p.date}`}
                x1={xAt(i)}
                y1={0}
                x2={xAt(i)}
                y2={chartH}
                stroke="var(--warning)"
                stroke-width="1"
                stroke-dasharray="2,3"
              />
              <title>{`${formatDate(p.date)}: the background identity changed here, so the view may differ from earlier points`}</title>
            </g>
          ) : null,
        )}

        {points.map((p, i) =>
          p.partial && p.score !== null ? (
            <circle
              key={`partial-${p.date}`}
              data-testid={`compliance-trend-partial-${p.date}`}
              cx={xAt(i)}
              cy={chartH - (p.score / 100) * chartH}
              r={4}
              fill="var(--surface)"
              stroke="var(--warning)"
              stroke-width="2"
            >
              <title>{`${formatDate(p.date)}: ${p.score.toFixed(1)}% (${partialNote(p)})`}</title>
            </circle>
          ) : null,
        )}

        {points.map((p, i) =>
          p.gapReason !== undefined ? (
            <g
              key={`gap-${p.date}`}
              data-testid={`compliance-trend-gap-${p.date}`}
            >
              <line
                x1={xAt(i) - 3}
                y1={chartH - 6}
                x2={xAt(i) + 3}
                y2={chartH}
                stroke="var(--error)"
                stroke-width="2"
              />
              <line
                x1={xAt(i) + 3}
                y1={chartH - 6}
                x2={xAt(i) - 3}
                y2={chartH}
                stroke="var(--error)"
                stroke-width="2"
              />
              <title>{`${formatDate(p.date)}: ${gapMessage(p.gapReason)}`}</title>
            </g>
          ) : null,
        )}

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
              {hoveredPoint.score?.toFixed(1)}%
              {hoveredPoint.partial ? " (partial)" : ""}
            </text>
          </g>
        )}

        {labelIndices.map((idx) => (
          <text
            key={idx}
            x={xAt(idx)}
            y={chartH + 20}
            fill="var(--text-muted)"
            font-size="10"
            text-anchor="middle"
          >
            {formatDate(points[idx].date)}
          </text>
        ))}
      </g>
    </svg>
  );
}

/**
 * Whether err is the history route answering 501 `remote_history_unsupported`.
 * Current backends serve remote history, but an older one still answers this.
 * It is a standing fact about the selection, not a failed read, so it is
 * worded as one and offers no retry.
 */
function isRemoteHistoryUnsupported(err: unknown): boolean {
  return (
    err instanceof ApiError &&
    err.status === 501 &&
    err.reason === "remote_history_unsupported"
  );
}

/**
 * Whether err is a remote cluster with no named background identity (R4):
 * 404 `compliance_history_not_configured`. Also a standing fact, not a failure.
 */
function isNotConfigured(err: unknown): boolean {
  return (
    err instanceof ApiError &&
    err.status === 404 &&
    err.reason === "compliance_history_not_configured"
  );
}

export default function ComplianceTrendChart() {
  const points = useSignal<FilledPoint[]>([]);
  const loading = useSignal(true);
  const error = useSignal<string | null>(null);
  const unsupported = useSignal(false);
  const notConfigured = useSignal(false);
  const range = useSignal<TimeRange>(30);
  const hoverIdx = useSignal<number | null>(null);
  // A late reply for a range or cluster the operator has left must not
  // overwrite the current one.
  const requestSeq = useRef(0);

  const fetchData = useCallback(async (days: TimeRange) => {
    if (!IS_BROWSER) return;
    const seq = ++requestSeq.current;
    loading.value = true;
    error.value = null;
    unsupported.value = false;
    notConfigured.value = false;
    try {
      // apiGet returns the whole parsed body, so the top-level `gaps` key
      // beside `data` is readable here (KTD6).
      const res = (await apiGet<TrendPoint[]>(
        `/v1/policies/compliance/history?days=${days}`,
      )) as { data: TrendPoint[]; gaps?: TrendGap[] };
      if (seq !== requestSeq.current) return;
      const raw = Array.isArray(res.data) ? res.data : [];
      const gaps = Array.isArray(res.gaps) ? res.gaps : [];
      points.value = fillDateGaps(
        raw,
        gaps,
        new Date().toISOString().slice(0, 10),
      );
    } catch (err) {
      if (seq !== requestSeq.current) return;
      if (isRemoteHistoryUnsupported(err)) {
        unsupported.value = true;
      } else if (isNotConfigured(err)) {
        notConfigured.value = true;
      } else {
        error.value = "Failed to load compliance trend data";
      }
      points.value = [];
    } finally {
      if (seq === requestSeq.current) loading.value = false;
    }
  }, []);

  // Reading selectedCluster.value subscribes the island; the refetch keys on
  // it so a switch shows the new cluster's trend, never the previous one's.
  const clusterId = selectedCluster.value;
  useEffect(() => {
    fetchData(range.value);
  }, [range.value, clusterId]);

  const realPoints = points.value.filter((p) => p.score !== null);
  // Trend figures count from the latest identity change (KTD9); earlier
  // points stay drawn.
  const trendPoints = realPoints.slice(trendStartIndex(realPoints));
  const identityChanged = trendPoints.length < realPoints.length;
  const gapDays = points.value.filter((p) => p.gapReason !== undefined);
  const current =
    realPoints.length > 0 ? realPoints[realPoints.length - 1].score! : null;
  const first = trendPoints.length > 0 ? trendPoints[0].score! : null;
  const delta = current !== null && first !== null ? current - first : null;
  const best =
    trendPoints.length > 0
      ? Math.max(...trendPoints.map((p) => p.score!))
      : null;
  const worst =
    trendPoints.length > 0
      ? Math.min(...trendPoints.map((p) => p.score!))
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

      {!loading.value && unsupported.value && (
        <p
          data-testid="compliance-trend-remote-unsupported"
          class="text-sm text-text-muted py-12 text-center"
        >
          Compliance history is recorded for the local cluster only. Select the
          local cluster to see the trend.
        </p>
      )}

      {!loading.value && notConfigured.value && (
        <div
          data-testid="compliance-trend-not-configured"
          class="text-sm text-text-muted py-12 text-center"
        >
          <p>Compliance history is not configured for this cluster.</p>
          <p class="mt-2">
            Name a background identity for it in the{" "}
            <a href="/settings/clusters" class="text-brand hover:underline">
              cluster manager
            </a>{" "}
            to start recording.
          </p>
        </div>
      )}

      {!loading.value &&
        !error.value &&
        !unsupported.value &&
        !notConfigured.value &&
        realPoints.length === 0 &&
        gapDays.length === 0 && (
          <p class="text-sm text-text-muted py-12 text-center">
            Compliance trend data will appear after the first snapshot.
          </p>
        )}

      {!loading.value &&
        !error.value &&
        (realPoints.length > 0 || gapDays.length > 0) && (
          <>
            <div class="flex gap-6 mb-4 text-sm">
              {current !== null && (
                <div>
                  <span class="text-text-muted">Current:</span>
                  <span
                    class="font-semibold"
                    style={{ color: scoreColor(current) }}
                  >
                    {current.toFixed(1)}
                  </span>
                </div>
              )}
              {delta !== null && (
                <div>
                  <span class="text-text-muted">&#916;</span>
                  <span
                    data-testid="compliance-trend-delta"
                    class="font-semibold"
                    style={{
                      color: delta >= 0 ? "var(--success)" : "var(--error)",
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
                  <span
                    data-testid="compliance-trend-best"
                    class="font-semibold text-text-primary"
                  >
                    {best.toFixed(1)}
                  </span>
                </div>
              )}
              {worst !== null && (
                <div>
                  <span class="text-text-muted">Worst:</span>
                  <span
                    data-testid="compliance-trend-worst"
                    class="font-semibold text-text-primary"
                  >
                    {worst.toFixed(1)}
                  </span>
                </div>
              )}
            </div>

            {identityChanged && (
              <p
                data-testid="compliance-trend-identity-note"
                class="text-xs text-text-muted mb-2"
              >
                The background identity changed, so the figures above count only
                points since the latest change.
              </p>
            )}

            <TrendChart
              points={points.value}
              hoverIdx={hoverIdx.value}
              onHover={(idx) => {
                hoverIdx.value = idx;
              }}
            />

            {gapDays.length > 0 && (
              <ul
                data-testid="compliance-trend-gaps"
                class="mt-3 text-xs text-text-muted space-y-1"
              >
                {gapDays.map((g) => (
                  <li key={g.date}>
                    {formatDate(g.date)}: {gapMessage(g.gapReason as string)}
                  </li>
                ))}
              </ul>
            )}
          </>
        )}
    </div>
  );
}
