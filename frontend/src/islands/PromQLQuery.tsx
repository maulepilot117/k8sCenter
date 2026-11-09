/** @jsxImportSource react */
import { type KeyboardEvent, useState } from "react";
import { ErrorBanner } from "@/components/ui/ErrorBanner.tsx";
import { SparklineChart } from "@/components/ui/SparklineChart.tsx";
import { apiGet } from "@/lib/api.ts";
import { useIsClient } from "@/src/lib/use-is-client.ts";

interface QueryResult {
  resultType: string;
  result: Array<{
    metric: Record<string, string>;
    value?: [number, string];
    values?: Array<[number, string]>;
  }>;
  warnings?: string[];
}

/** A range series reduced to what its table cell shows. Prometheus encodes
 * sample values as strings, and "NaN" / "+Inf" are legal; those are dropped
 * from the numbers but still counted as samples. */
export interface SeriesSummary {
  samples: number;
  numbers: number[];
  latest: number | null;
  min: number | null;
  max: number | null;
}

export function summarizeSeries(
  values: Array<[number, string]>,
): SeriesSummary {
  const numbers: number[] = [];
  let min: number | null = null;
  let max: number | null = null;
  for (const [, raw] of values) {
    const v = Number.parseFloat(raw);
    if (!Number.isFinite(v)) continue;
    numbers.push(v);
    if (min === null || v < min) min = v;
    if (max === null || v > max) max = v;
  }
  return {
    samples: values.length,
    numbers,
    latest: numbers.length > 0 ? (numbers.at(-1) as number) : null,
    min,
    max,
  };
}

function formatSample(v: number | null): string {
  if (v === null) return "N/A";
  return Number.isInteger(v) ? String(v) : v.toPrecision(4);
}

/** A range series as a trend line with its latest value and range. */
function RangeCell({ values }: { values: Array<[number, string]> }) {
  const s = summarizeSeries(values);
  return (
    <div className="flex items-center gap-3">
      <div className="w-48 shrink-0">
        <SparklineChart data={s.numbers} color="var(--accent)" height={28} />
      </div>
      <div>
        <div className="text-text-primary">{formatSample(s.latest)}</div>
        <div className="text-text-muted">
          {s.min !== null
            ? `${formatSample(s.min)} – ${formatSample(s.max)} · `
            : ""}
          {s.samples} samples
        </div>
      </div>
    </div>
  );
}

const TIME_RANGES = [
  { label: "Last 1h", value: "1h" },
  { label: "Last 6h", value: "6h" },
  { label: "Last 24h", value: "24h" },
  { label: "Last 7d", value: "7d" },
];

function subtractDuration(d: string): Date {
  const now = new Date();
  const val = parseInt(d, 10);
  const unit = d.replace(/\d+/, "");
  switch (unit) {
    case "h":
      now.setHours(now.getHours() - val);
      break;
    case "d":
      now.setDate(now.getDate() - val);
      break;
  }
  return now;
}

export default function PromQLQuery() {
  const [query, setQuery] = useState("");
  const [queryType, setQueryType] = useState<"instant" | "range">("instant");
  const [timeRange, setTimeRange] = useState("1h");
  const [result, setResult] = useState<QueryResult | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const isClient = useIsClient();

  if (!isClient) return null;

  async function runQuery() {
    const q = query.trim();
    if (!q) return;

    setLoading(true);
    setError(null);
    setResult(null);

    try {
      if (queryType === "instant") {
        const res = await apiGet<QueryResult>(
          `/v1/monitoring/query?query=${encodeURIComponent(q)}`,
        );
        setResult(res.data);
      } else {
        const end = new Date();
        const start = subtractDuration(timeRange);
        // Step: ~200 data points
        const stepMs = (end.getTime() - start.getTime()) / 200;
        const step = `${Math.max(Math.round(stepMs / 1000), 15)}s`;

        const params = new URLSearchParams({
          query: q,
          start: start.toISOString(),
          end: end.toISOString(),
          step,
        });
        const res = await apiGet<QueryResult>(
          `/v1/monitoring/query_range?${params}`,
        );
        setResult(res.data);
      }
    } catch (err) {
      setError((err as Error).message ?? "Query failed");
    } finally {
      setLoading(false);
    }
  }

  function handleKeyDown(e: KeyboardEvent<HTMLTextAreaElement>) {
    if (e.key === "Enter" && (e.ctrlKey || e.metaKey)) {
      runQuery();
    }
  }

  return (
    <div className="space-y-4">
      {/* Query input */}
      <div className="space-y-3">
        <div>
          <label className="block text-sm font-medium text-text-secondary mb-1">
            PromQL Expression
          </label>
          <textarea
            value={query}
            onChange={(e) => setQuery(e.currentTarget.value)}
            onKeyDown={handleKeyDown}
            placeholder='up{job="prometheus"}'
            rows={3}
            className="w-full rounded-md border border-border-primary bg-surface px-3 py-2 font-mono text-sm text-text-primary placeholder:text-text-muted focus:border-brand focus:outline-none focus:ring-1 focus:ring-brand text-text-primary"
          />
          <p className="mt-1 text-xs text-text-muted">
            Press Ctrl+Enter to run query
          </p>
        </div>

        {/* Query type and controls */}
        <div className="flex flex-wrap items-center gap-3">
          <div className="flex rounded-md border border-border-primary">
            <button
              type="button"
              onClick={() => setQueryType("instant")}
              className={`px-3 py-1.5 text-sm ${
                queryType === "instant"
                  ? "bg-brand"
                  : "text-text-secondary hover:bg-hover"
              }`}
              style={
                queryType === "instant"
                  ? { color: "var(--bg-base)" }
                  : undefined
              }
            >
              Instant
            </button>
            <button
              type="button"
              onClick={() => setQueryType("range")}
              className={`px-3 py-1.5 text-sm border-l border-border-primary ${
                queryType === "range"
                  ? "bg-brand"
                  : "text-text-secondary hover:bg-hover"
              }`}
              style={
                queryType === "range" ? { color: "var(--bg-base)" } : undefined
              }
            >
              Range
            </button>
          </div>

          {queryType === "range" && (
            <select
              value={timeRange}
              onChange={(e) => setTimeRange(e.currentTarget.value)}
              className="rounded-md border border-border-primary bg-surface px-3 py-1.5 text-sm text-text-secondary text-text-secondary"
            >
              {TIME_RANGES.map((r) => (
                <option key={r.value} value={r.value}>
                  {r.label}
                </option>
              ))}
            </select>
          )}

          <button
            type="button"
            onClick={runQuery}
            disabled={loading || !query.trim()}
            className="rounded-md bg-brand px-4 py-1.5 text-sm font-medium hover:bg-brand/90 disabled:opacity-50"
            style={{ color: "var(--bg-base)" }}
          >
            {loading ? "Running..." : "Run Query"}
          </button>
        </div>
      </div>

      {/* Error */}
      {error && <ErrorBanner message={error} />}

      {/* Results */}
      {result && (
        <div className="space-y-2">
          <div className="flex items-center gap-2 text-sm text-text-muted">
            <span>Type: {result.resultType}</span>
            <span>|</span>
            <span>{result.result.length} result(s)</span>
            {result.warnings && result.warnings.length > 0 && (
              <span className="text-warning">
                | {result.warnings.length} warning(s)
              </span>
            )}
          </div>

          {result.warnings?.map((w, i) => (
            <div
              // biome-ignore lint/suspicious/noArrayIndexKey: warnings are plain strings that can repeat, and the list is replaced whole on each query
              key={i}
              className="rounded-md border border-warning bg-warning-dim px-3 py-2 text-sm text-warning"
            >
              {w}
            </div>
          ))}

          {result.result.length === 0 ? (
            <p className="py-8 text-center text-sm text-text-muted">
              No results
            </p>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full text-sm">
                <thead>
                  <tr className="border-b border-border-primary">
                    <th className="px-3 py-2 text-left font-medium text-text-muted">
                      Metric
                    </th>
                    <th className="px-3 py-2 text-left font-medium text-text-muted">
                      Value
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {result.result.map((r, i) => {
                    const metricLabel = Object.entries(r.metric)
                      .map(([k, v]) => `${k}="${v}"`)
                      .join(",");
                    const value = r.value ? (
                      r.value[1]
                    ) : r.values ? (
                      <RangeCell values={r.values} />
                    ) : (
                      "N/A"
                    );
                    return (
                      <tr
                        // biome-ignore lint/suspicious/noArrayIndexKey: series have no unique id, and the table is replaced whole on each query
                        key={i}
                        className="border-b border-border-subtle"
                      >
                        <td className="px-3 py-2 font-mono text-xs text-text-secondary">
                          {`{${metricLabel}}` || "{}"}
                        </td>
                        <td className="px-3 py-2 font-mono text-xs text-text-primary">
                          {value}
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          )}
        </div>
      )}
    </div>
  );
}
