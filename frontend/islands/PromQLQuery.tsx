import { useSignal } from"@preact/signals";
import { IS_BROWSER } from"fresh/runtime";
import { apiGet } from"@/lib/api.ts";

interface QueryResult {
 resultType: string;
 result: Array<{
 metric: Record<string, string>;
 value?: [number, string];
 values?: Array<[number, string]>;
 }>;
 warnings?: string[];
}

const TIME_RANGES = [
 { label:"Last 1h", value:"1h" },
 { label:"Last 6h", value:"6h" },
 { label:"Last 24h", value:"24h" },
 { label:"Last 7d", value:"7d" },
];

function subtractDuration(d: string): Date {
 const now = new Date();
 const val = parseInt(d);
 const unit = d.replace(/\d+/,"");
 switch (unit) {
 case"h":
 now.setHours(now.getHours() - val);
 break;
 case"d":
 now.setDate(now.getDate() - val);
 break;
 }
 return now;
}

export default function PromQLQuery() {
 const query = useSignal("");
 const queryType = useSignal<"instant" |"range">("instant");
 const timeRange = useSignal("1h");
 const result = useSignal<QueryResult | null>(null);
 const loading = useSignal(false);
 const error = useSignal<string | null>(null);

 if (!IS_BROWSER) return null;

 async function runQuery() {
 const q = query.value.trim();
 if (!q) return;

 loading.value = true;
 error.value = null;
 result.value = null;

 try {
 if (queryType.value ==="instant") {
 const res = await apiGet<QueryResult>(
 `/v1/monitoring/query?query=${encodeURIComponent(q)}`,
 );
 result.value = res.data;
 } else {
 const end = new Date();
 const start = subtractDuration(timeRange.value);
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
 result.value = res.data;
 }
 } catch (err) {
 error.value = (err as Error).message ??"Query failed";
 } finally {
 loading.value = false;
 }
 }

 function handleKeyDown(e: KeyboardEvent) {
 if (e.key ==="Enter" && (e.ctrlKey || e.metaKey)) {
 runQuery();
 }
 }

 return (
 <div class="space-y-4">
 {/* Query input */}
 <div class="space-y-3">
 <div>
 <label class="block text-sm font-medium text-text-secondary mb-1">
 PromQL Expression
 </label>
 <textarea
 value={query.value}
 onInput={(e) =>
 query.value = (e.target as HTMLTextAreaElement).value}
 onKeyDown={handleKeyDown}
 placeholder='up{job="prometheus"}'
 rows={3}
 class="w-full rounded-md border border-border-primary bg-surface px-3 py-2 font-mono text-sm text-text-primary placeholder:text-text-muted focus:border-brand focus:outline-none focus:ring-1 focus:ring-brand text-text-primary"
 />
 <p class="mt-1 text-xs text-text-muted">
 Press Ctrl+Enter to run query
 </p>
 </div>

 {/* Query type and controls */}
 <div class="flex flex-wrap items-center gap-3">
 <div class="flex rounded-md border border-border-primary">
 <button
 type="button"
 onClick={() => queryType.value ="instant"}
 class={`px-3 py-1.5 text-sm ${
 queryType.value ==="instant"
 ?"bg-brand text-white"
 :"text-text-secondary hover:bg-hover"
 }`}
 >
 Instant
 </button>
 <button
 type="button"
 onClick={() => queryType.value ="range"}
 class={`px-3 py-1.5 text-sm border-l border-border-primary ${
 queryType.value ==="range"
 ?"bg-brand text-white"
 :"text-text-secondary hover:bg-hover"
 }`}
 >
 Range
 </button>
 </div>

 {queryType.value ==="range" && (
 <select
 value={timeRange.value}
 onChange={(e) =>
 timeRange.value = (e.target as HTMLSelectElement).value}
 class="rounded-md border border-border-primary bg-surface px-3 py-1.5 text-sm text-text-secondary text-text-secondary"
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
 disabled={loading.value || !query.value.trim()}
 class="rounded-md bg-brand px-4 py-1.5 text-sm font-medium text-white hover:bg-brand/90 disabled:opacity-50"
 >
 {loading.value ?"Running..." :"Run Query"}
 </button>
 </div>
 </div>

 {/* Error */}
 {error.value && (
 <div class="rounded-md border border-danger bg-danger-dim px-4 py-3 text-sm text-danger">
 {error.value}
 </div>
 )}

 {/* Results */}
 {result.value && (
 <div class="space-y-2">
 <div class="flex items-center gap-2 text-sm text-text-muted">
 <span>Type: {result.value.resultType}</span>
 <span>|</span>
 <span>{result.value.result.length} result(s)</span>
 {result.value.warnings && result.value.warnings.length > 0 && (
 <span class="text-amber-500">
 | {result.value.warnings.length} warning(s)
 </span>
 )}
 </div>

 {result.value.warnings?.map((w, i) => (
 <div
 key={i}
 class="rounded-md border border-warning bg-warning-dim px-3 py-2 text-sm text-warning"
 >
 {w}
 </div>
 ))}

 {result.value.result.length === 0
 ? (
 <p class="py-8 text-center text-sm text-text-muted">
 No results
 </p>
 )
 : (
 <div class="overflow-x-auto">
 <table class="w-full text-sm">
 <thead>
 <tr class="border-b border-border-primary">
 <th class="px-3 py-2 text-left font-medium text-text-muted">
 Metric
 </th>
 <th class="px-3 py-2 text-left font-medium text-text-muted">
 Value
 </th>
 </tr>
 </thead>
 <tbody>
 {result.value.result.map((r, i) => {
 const metricLabel = Object.entries(r.metric)
 .map(([k, v]) => `${k}="${v}"`)
 .join(",");
 const value = r.value
 ? r.value[1]
 : r.values
 ? `${r.values.length} samples`
 :"N/A";
 return (
 <tr
 key={i}
 class="border-b border-border-subtle"
 >
 <td class="px-3 py-2 font-mono text-xs text-text-secondary">
 {`{${metricLabel}}` ||"{}"}
 </td>
 <td class="px-3 py-2 font-mono text-xs text-text-primary">
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
