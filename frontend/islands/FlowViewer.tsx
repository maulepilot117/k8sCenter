import { useSignal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiGet } from "@/lib/api.ts";
import { selectedNamespace } from "@/lib/namespace.ts";
import { Button } from "@/components/ui/Button.tsx";

interface FlowRecord {
  time: string;
  verdict: string;
  dropReason?: string;
  direction: string;
  srcNamespace: string;
  srcPod: string;
  dstNamespace: string;
  dstPod: string;
  protocol: string;
  dstPort?: number;
}

const VERDICT_OPTIONS = ["", "FORWARDED", "DROPPED", "ERROR", "AUDIT"];

function verdictBadgeClass(verdict: string): string {
  switch (verdict) {
    case "FORWARDED":
      return "bg-green-100 text-green-800 dark:bg-green-900/30 dark:text-green-300";
    case "DROPPED":
      return "bg-red-100 text-red-800 dark:bg-red-900/30 dark:text-red-300";
    case "AUDIT":
      return "bg-amber-100 text-amber-800 dark:bg-amber-900/30 dark:text-amber-300";
    case "ERROR":
      return "bg-red-200 text-red-900 dark:bg-red-900/50 dark:text-red-200";
    default:
      return "bg-slate-100 text-slate-700 dark:bg-slate-800 dark:text-slate-300";
  }
}

export default function FlowViewer() {
  const namespace = useSignal(
    IS_BROWSER && selectedNamespace.value !== "all"
      ? selectedNamespace.value
      : "default",
  );
  const namespaces = useSignal<string[]>(["default"]);
  const verdict = useSignal("");
  const flows = useSignal<FlowRecord[]>([]);
  const loading = useSignal(false);
  const error = useSignal<string | null>(null);

  // Fetch namespaces
  useEffect(() => {
    if (!IS_BROWSER) return;
    apiGet<Array<{ metadata: { name: string } }>>("/v1/resources/namespaces")
      .then((resp) => {
        if (Array.isArray(resp.data)) {
          namespaces.value = resp.data.map((ns) => ns.metadata.name).sort();
        }
      })
      .catch(() => {});
  }, []);

  const fetchFlows = async () => {
    if (!IS_BROWSER) return;
    loading.value = true;
    error.value = null;
    try {
      let url = `/v1/networking/hubble/flows?namespace=${
        encodeURIComponent(namespace.value)
      }&limit=200`;
      if (verdict.value) {
        url += `&verdict=${encodeURIComponent(verdict.value)}`;
      }
      const resp = await apiGet<FlowRecord[]>(url);
      flows.value = Array.isArray(resp.data) ? resp.data : [];
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : "Failed to fetch flows";
      error.value = msg;
      flows.value = [];
    } finally {
      loading.value = false;
    }
  };

  // Fetch on mount and when filters change
  useEffect(() => {
    if (!IS_BROWSER) return;
    fetchFlows();
  }, [namespace.value, verdict.value]);

  if (!IS_BROWSER) {
    return (
      <div class="p-6">
        <h1 class="text-2xl font-semibold text-slate-900 dark:text-white">
          Network Flows
        </h1>
      </div>
    );
  }

  return (
    <div class="p-6">
      <div class="flex items-center justify-between mb-6">
        <h1 class="text-2xl font-semibold text-slate-900 dark:text-white">
          Network Flows
        </h1>
        <Button
          variant="secondary"
          onClick={fetchFlows}
          disabled={loading.value}
        >
          {loading.value ? "Loading..." : "Refresh"}
        </Button>
      </div>

      {/* Filters */}
      <div class="flex items-center gap-4 mb-4">
        <div>
          <label class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1">
            Namespace
          </label>
          <select
            value={namespace.value}
            onChange={(e) =>
              namespace.value = (e.target as HTMLSelectElement).value}
            class="rounded-md border border-slate-300 dark:border-slate-600 bg-white dark:bg-slate-800 px-3 py-1.5 text-sm text-slate-900 dark:text-white"
          >
            {namespaces.value.map((ns) => (
              <option key={ns} value={ns}>{ns}</option>
            ))}
          </select>
        </div>
        <div>
          <label class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1">
            Verdict
          </label>
          <select
            value={verdict.value}
            onChange={(e) =>
              verdict.value = (e.target as HTMLSelectElement).value}
            class="rounded-md border border-slate-300 dark:border-slate-600 bg-white dark:bg-slate-800 px-3 py-1.5 text-sm text-slate-900 dark:text-white"
          >
            {VERDICT_OPTIONS.map((v) => (
              <option key={v} value={v}>{v || "All"}</option>
            ))}
          </select>
        </div>
        <div class="text-xs text-slate-500 dark:text-slate-400 self-end pb-1.5">
          {flows.value.length} flows
        </div>
      </div>

      {error.value && (
        <div class="mb-4 rounded-md bg-red-50 dark:bg-red-900/20 p-3 border border-red-200 dark:border-red-800">
          <p class="text-sm text-red-800 dark:text-red-200">{error.value}</p>
        </div>
      )}

      {/* Flow table */}
      <div class="overflow-x-auto rounded-lg border border-slate-200 dark:border-slate-700">
        <table class="min-w-full text-sm">
          <thead>
            <tr class="bg-slate-50 dark:bg-slate-800/50 text-left text-slate-600 dark:text-slate-400">
              <th class="py-2 px-3 font-medium">Time</th>
              <th class="py-2 px-3 font-medium">Direction</th>
              <th class="py-2 px-3 font-medium">Source</th>
              <th class="py-2 px-3 font-medium">Destination</th>
              <th class="py-2 px-3 font-medium">Protocol</th>
              <th class="py-2 px-3 font-medium">Verdict</th>
            </tr>
          </thead>
          <tbody>
            {flows.value.length === 0 && !loading.value && (
              <tr>
                <td
                  colSpan={6}
                  class="py-8 px-3 text-center text-slate-500 dark:text-slate-400"
                >
                  {error.value
                    ? "Failed to load flows"
                    : "No flows found. Hubble may not be enabled or there is no traffic in this namespace."}
                </td>
              </tr>
            )}
            {flows.value.map((f, i) => (
              <tr
                key={i}
                class="border-t border-slate-100 dark:border-slate-700/50 hover:bg-slate-50 dark:hover:bg-slate-800/30"
              >
                <td class="py-1.5 px-3 text-slate-500 dark:text-slate-400 whitespace-nowrap font-mono text-xs">
                  {formatTime(f.time)}
                </td>
                <td class="py-1.5 px-3">
                  <span class="text-xs text-slate-600 dark:text-slate-400">
                    {f.direction === "INGRESS" ? "\u2B07" : "\u2B06"}{" "}
                    {f.direction}
                  </span>
                </td>
                <td class="py-1.5 px-3 whitespace-nowrap">
                  <span class="text-slate-900 dark:text-white">
                    {f.srcNamespace ? `${f.srcNamespace}/` : ""}
                    {f.srcPod || "external"}
                  </span>
                </td>
                <td class="py-1.5 px-3 whitespace-nowrap">
                  <span class="text-slate-900 dark:text-white">
                    {f.dstNamespace ? `${f.dstNamespace}/` : ""}
                    {f.dstPod || "external"}
                    {f.dstPort ? `:${f.dstPort}` : ""}
                  </span>
                </td>
                <td class="py-1.5 px-3 text-slate-600 dark:text-slate-400">
                  {f.protocol}
                </td>
                <td class="py-1.5 px-3">
                  <span
                    class={`inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium ${
                      verdictBadgeClass(f.verdict)
                    }`}
                    title={f.dropReason || undefined}
                  >
                    {f.verdict}
                  </span>
                  {f.dropReason && (
                    <span class="ml-1 text-xs text-red-500">
                      {f.dropReason}
                    </span>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function formatTime(iso: string): string {
  try {
    const d = new Date(iso);
    return d.toLocaleTimeString(undefined, {
      hour12: false,
      hour: "2-digit",
      minute: "2-digit",
      second: "2-digit",
      fractionalSecondDigits: 3,
    });
  } catch {
    return iso;
  }
}
