/** @jsxImportSource react */
import { useCallback, useEffect, useRef, useState } from "react";
import {
  CartesianGrid,
  Line,
  LineChart,
  ReferenceDot,
  ReferenceLine,
  Tooltip,
  YAxis,
} from "recharts";
import { TimeXAxis } from "@/components/charts/ChartAxes.tsx";
import ChartFrame from "@/components/charts/ChartFrame.tsx";
import {
  AXIS_CHART_PROPS,
  AXIS_TICK_STYLE,
  CHART_TOKENS,
  formatValue,
  TOOLTIP_PROPS,
  timeTickFormatter,
} from "@/components/charts/chart-theme.ts";
import { Button } from "@/components/ui/Button.tsx";
import { Spinner } from "@/components/ui/Spinner.tsx";
import { ApiError, apiGet } from "@/lib/api.ts";
import { trendStartIndex } from "@/lib/dashboard/severity.ts";
import { scoreColor } from "@/lib/score-color.ts";
import { selectedCluster } from "@/src/lib/cluster.ts";
import { IS_BROWSER } from "@/src/lib/is-browser.ts";
import { useSignalValue } from "@/src/lib/use-signal-value.ts";

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

/** One plain sentence per gap reason the recorder writes. */
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

type MarkerKind = "partial" | "gap" | "identity-change";

/** A day the chart flags, with the sentence its marker and list entry carry. */
interface Marker {
  key: string;
  kind: MarkerKind;
  date: string;
  /** Local midnight of the day, the marker's place on the time axis. */
  t: number;
  /** Where the marker sits on the value axis; a gap sits on the baseline. */
  y: number;
  text: string;
}

/** Local midnight of a calendar-day string, so axis ticks land on that day. */
function dayStart(dateStr: string): number {
  const [y, m, d] = dateStr.split("-").map(Number);
  return new Date(y, m - 1, d).getTime();
}

function buildMarkers(points: FilledPoint[]): Marker[] {
  const markers: Marker[] = [];
  for (const p of points) {
    const t = dayStart(p.date);
    if (p.score !== null && p.identityChanged) {
      markers.push({
        key: `identity-change-${p.date}`,
        kind: "identity-change",
        date: p.date,
        t,
        y: p.score,
        text: `${formatDate(p.date)}: the background identity changed here, so the view may differ from earlier points`,
      });
    }
    if (p.score !== null && p.partial) {
      markers.push({
        key: `partial-${p.date}`,
        kind: "partial",
        date: p.date,
        t,
        y: p.score,
        text: `${formatDate(p.date)}: ${p.score.toFixed(1)}% (${partialNote(p)})`,
      });
    }
    if (p.gapReason !== undefined) {
      markers.push({
        key: `gap-${p.date}`,
        kind: "gap",
        date: p.date,
        t,
        y: 0,
        text: `${formatDate(p.date)}: ${gapMessage(p.gapReason)}`,
      });
    }
  }
  return markers;
}

const SCORE_TICKS = [0, 25, 50, 75, 100];

function MarkerGlyph({ kind }: { kind: MarkerKind }) {
  if (kind === "partial") {
    return (
      <circle
        r={4}
        fill="var(--surface)"
        stroke="var(--warning)"
        strokeWidth={2}
      />
    );
  }
  if (kind === "identity-change") {
    return <circle r={3} fill="var(--warning)" />;
  }
  return (
    <g stroke="var(--error)" strokeWidth={2}>
      <line x1={-3} y1={-9} x2={3} y2={-3} />
      <line x1={3} y1={-9} x2={-3} y2={-3} />
    </g>
  );
}

function TrendChart({
  points,
  range,
}: {
  points: FilledPoint[];
  range: TimeRange;
}) {
  // Which marker's text is showing: set by hover, focus or tap, cleared by
  // leaving, blur or Escape.
  const [openKey, setOpenKey] = useState<string | null>(null);

  const markers = buildMarkers(points);
  const open = markers.find((m) => m.key === openKey);
  const end = dayStart(points[points.length - 1].date);
  const first = dayStart(points[0].date);
  // The window the operator picked, widened if a record falls before it.
  const start = Math.min(
    first,
    dayStart(addDays(points[points.length - 1].date, -(range - 1))),
  );
  const rows = points.map((p) => ({ t: dayStart(p.date), score: p.score }));
  const dateLabel = timeTickFormatter(end - start);

  return (
    <>
      <ChartFrame
        preset="axis"
        kind="compliance-trend"
        label={`Compliance score by day, ${formatDate(points[0].date)} to ${formatDate(points[points.length - 1].date)}`}
      >
        <LineChart {...AXIS_CHART_PROPS} data={rows}>
          <CartesianGrid
            vertical={false}
            stroke={CHART_TOKENS.grid}
            strokeDasharray="4 4"
          />
          <TimeXAxis dataKey="t" start={start} end={end} />
          <YAxis
            type="number"
            domain={[0, 100]}
            ticks={SCORE_TICKS}
            tickFormatter={(v: number) => formatValue(v, "percent")}
            tick={AXIS_TICK_STYLE}
            tickLine={false}
            axisLine={false}
            width={48}
          />
          <Tooltip
            {...TOOLTIP_PROPS}
            labelFormatter={(t) => dateLabel(Number(t))}
            formatter={(v) => formatValue(Number(v), "percent")}
          />
          <Line
            dataKey="score"
            name="Score"
            stroke={CHART_TOKENS.line}
            strokeWidth={2}
            dot={{ r: 2.5, fill: CHART_TOKENS.line, stroke: "none" }}
            connectNulls={false}
            isAnimationActive={false}
          />
          {markers
            .filter((m) => m.kind === "identity-change")
            .map((m) => (
              <ReferenceLine
                key={`line-${m.key}`}
                x={m.t}
                stroke="var(--warning)"
                strokeDasharray="2 3"
                ifOverflow="visible"
              />
            ))}
          {markers.map((m) => (
            <ReferenceDot
              key={m.key}
              x={m.t}
              y={m.y}
              r={4}
              ifOverflow="visible"
              shape={(p: { cx?: number; cy?: number }) => (
                <g
                  data-testid={`compliance-trend-marker-${m.kind}-${m.date}`}
                  transform={`translate(${p.cx ?? 0},${p.cy ?? 0})`}
                  role="button"
                  tabIndex={0}
                  aria-label={m.text}
                  className="cursor-pointer"
                  onFocus={() => setOpenKey(m.key)}
                  onBlur={() => setOpenKey((k) => (k === m.key ? null : k))}
                  onMouseEnter={() => setOpenKey(m.key)}
                  onMouseLeave={() =>
                    setOpenKey((k) => (k === m.key ? null : k))
                  }
                  onClick={() => setOpenKey(m.key)}
                  onKeyDown={(e) => {
                    if (e.key === "Escape") setOpenKey(null);
                  }}
                >
                  <circle r={10} fill="transparent" />
                  <MarkerGlyph kind={m.kind} />
                </g>
              )}
            />
          ))}
        </LineChart>
      </ChartFrame>

      <div className="mt-2 min-h-8">
        {open && (
          <p
            data-testid="compliance-trend-marker-tip"
            className="inline-block rounded-md border border-border-primary bg-elevated px-3 py-1.5 text-xs text-text-primary"
          >
            {open.text}
          </p>
        )}
      </div>

      {markers.length > 0 && (
        <ul className="sr-only">
          {markers.map((m) => (
            <li
              key={m.key}
              data-testid={`compliance-trend-${m.kind}-${m.date}`}
            >
              {m.text}
            </li>
          ))}
        </ul>
      )}
    </>
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
 * Whether err is a remote cluster with no named background identity:
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
  const [points, setPoints] = useState<FilledPoint[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [unsupported, setUnsupported] = useState(false);
  const [notConfigured, setNotConfigured] = useState(false);
  const [range, setRange] = useState<TimeRange>(30);
  // A late reply for a range or cluster the operator has left must not
  // overwrite the current one.
  const requestSeq = useRef(0);

  const fetchData = useCallback(async (days: TimeRange) => {
    if (!IS_BROWSER) return;
    const seq = ++requestSeq.current;
    setLoading(true);
    setError(null);
    setUnsupported(false);
    setNotConfigured(false);
    try {
      // apiGet returns the whole parsed body, so the top-level `gaps` key
      // beside `data` is readable here.
      const res = (await apiGet<TrendPoint[]>(
        `/v1/policies/compliance/history?days=${days}`,
      )) as { data: TrendPoint[]; gaps?: TrendGap[] };
      if (seq !== requestSeq.current) return;
      const raw = Array.isArray(res.data) ? res.data : [];
      const gaps = Array.isArray(res.gaps) ? res.gaps : [];
      setPoints(fillDateGaps(raw, gaps, new Date().toISOString().slice(0, 10)));
    } catch (err) {
      if (seq !== requestSeq.current) return;
      if (isRemoteHistoryUnsupported(err)) {
        setUnsupported(true);
      } else if (isNotConfigured(err)) {
        setNotConfigured(true);
      } else {
        setError("Failed to load compliance trend data");
      }
      setPoints([]);
    } finally {
      if (seq === requestSeq.current) setLoading(false);
    }
  }, []);

  const clusterId = useSignalValue(selectedCluster);
  // biome-ignore lint/correctness/useExhaustiveDependencies: clusterId is not read by the effect; a cluster switch is meant to refetch
  useEffect(() => {
    fetchData(range);
  }, [fetchData, range, clusterId]);

  const realPoints = points.filter((p) => p.score !== null);
  // Trend figures count from the latest identity change; earlier
  // points stay drawn.
  const trendPoints = realPoints.slice(trendStartIndex(realPoints));
  const identityChanged = trendPoints.length < realPoints.length;
  const gapDays = points.filter((p) => p.gapReason !== undefined);
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
    <div className="rounded-lg border border-border-primary p-6 bg-elevated mb-8">
      <div className="flex items-center justify-between mb-4">
        <h3 className="text-lg font-semibold text-text-primary">
          Compliance Trend
        </h3>
        <div className="flex gap-1">
          {ranges.map((r) => (
            <button
              type="button"
              key={r}
              className={`px-3 py-1 text-xs font-medium rounded border transition-colors ${
                range === r
                  ? "bg-brand/10 text-brand border-brand/30"
                  : "text-text-secondary border-border-primary hover:text-text-primary"
              }`}
              onClick={() => {
                setRange(r);
              }}
            >
              {r}d
            </button>
          ))}
        </div>
      </div>

      {loading && (
        <div className="flex items-center justify-center py-16">
          <Spinner />
        </div>
      )}

      {!loading && error && (
        <div className="flex flex-col items-center gap-3 py-12">
          <p className="text-sm text-text-muted">{error}</p>
          <Button variant="ghost" onClick={() => fetchData(range)}>
            Retry
          </Button>
        </div>
      )}

      {!loading && unsupported && (
        <p
          data-testid="compliance-trend-remote-unsupported"
          className="text-sm text-text-muted py-12 text-center"
        >
          Compliance history is recorded for the local cluster only. Select the
          local cluster to see the trend.
        </p>
      )}

      {!loading && notConfigured && (
        <div
          data-testid="compliance-trend-not-configured"
          className="text-sm text-text-muted py-12 text-center"
        >
          <p>Compliance history is not configured for this cluster.</p>
          <p className="mt-2">
            Name a background identity for it in the{" "}
            <a href="/settings/clusters" className="text-brand hover:underline">
              cluster manager
            </a>{" "}
            to start recording.
          </p>
        </div>
      )}

      {!loading &&
        !error &&
        !unsupported &&
        !notConfigured &&
        realPoints.length === 0 &&
        gapDays.length === 0 && (
          <p className="text-sm text-text-muted py-12 text-center">
            Compliance trend data will appear after the first snapshot.
          </p>
        )}

      {!loading && !error && (realPoints.length > 0 || gapDays.length > 0) && (
        <>
          <div className="flex gap-6 mb-4 text-sm">
            {current !== null && (
              <div>
                <span className="text-text-muted">Current:</span>
                <span
                  className="font-semibold"
                  style={{ color: scoreColor(current) }}
                >
                  {current.toFixed(1)}
                </span>
              </div>
            )}
            {delta !== null && (
              <div>
                <span className="text-text-muted">&#916;</span>
                <span
                  data-testid="compliance-trend-delta"
                  className="font-semibold"
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
                <span className="text-text-muted">Best:</span>
                <span
                  data-testid="compliance-trend-best"
                  className="font-semibold text-text-primary"
                >
                  {best.toFixed(1)}
                </span>
              </div>
            )}
            {worst !== null && (
              <div>
                <span className="text-text-muted">Worst:</span>
                <span
                  data-testid="compliance-trend-worst"
                  className="font-semibold text-text-primary"
                >
                  {worst.toFixed(1)}
                </span>
              </div>
            )}
          </div>

          {identityChanged && (
            <p
              data-testid="compliance-trend-identity-note"
              className="text-xs text-text-muted mb-2"
            >
              The background identity changed, so the figures above count only
              points since the latest change.
            </p>
          )}

          <TrendChart points={points} range={range} />

          {gapDays.length > 0 && (
            <ul
              data-testid="compliance-trend-gaps"
              className="mt-3 text-xs text-text-muted space-y-1"
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
