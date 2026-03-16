import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { useEffect, useRef } from "preact/hooks";
import { apiGet } from "@/lib/api.ts";
import { Button } from "@/components/ui/Button.tsx";

interface LogViewerProps {
  namespace: string;
  pod: string;
  containers: string[];
}

interface LogResponse {
  lines: string[] | null;
  container: string;
  count: number;
}

export default function LogViewer(
  { namespace, pod, containers }: LogViewerProps,
) {
  const selectedContainer = useSignal(containers[0] || "");
  const previous = useSignal(false);
  const autoRefresh = useSignal(false);
  const lines = useSignal<string[]>([]);
  const loading = useSignal(true);
  const error = useSignal("");
  const preRef = useRef<HTMLPreElement>(null);

  useEffect(() => {
    if (!IS_BROWSER) return;
    fetchLogs();

    let interval: number | undefined;
    if (autoRefresh.value) {
      interval = setInterval(fetchLogs, 5_000);
    }
    return () => {
      if (interval) clearInterval(interval);
    };
  }, [selectedContainer.value, previous.value, autoRefresh.value]);

  async function fetchLogs() {
    error.value = "";
    try {
      const params = new URLSearchParams({
        container: selectedContainer.value,
        tailLines: "500",
        previous: String(previous.value),
        timestamps: "true",
      });
      const res = await apiGet<LogResponse>(
        `/v1/resources/pods/${namespace}/${pod}/logs?${params}`,
      );
      lines.value = res.data.lines ?? [];
      loading.value = false;

      // Auto-scroll to bottom
      requestAnimationFrame(() => {
        if (preRef.current) {
          preRef.current.scrollTop = preRef.current.scrollHeight;
        }
      });
    } catch (err) {
      error.value = err instanceof Error ? err.message : "Failed to fetch logs";
      loading.value = false;
    }
  }

  if (!IS_BROWSER) return null;

  return (
    <div class="flex flex-col gap-3">
      {/* Controls */}
      <div class="flex flex-wrap items-center gap-3">
        {containers.length > 1 && (
          <select
            value={selectedContainer.value}
            onChange={(e) => {
              selectedContainer.value = (e.target as HTMLSelectElement).value;
              loading.value = true;
            }}
            class="rounded-md border border-slate-300 bg-white px-2 py-1 text-sm dark:border-slate-600 dark:bg-slate-700 dark:text-slate-200"
          >
            {containers.map((c) => <option key={c} value={c}>{c}</option>)}
          </select>
        )}

        <label class="flex items-center gap-1.5 text-sm text-slate-600 dark:text-slate-300">
          <input
            type="checkbox"
            checked={previous.value}
            onChange={(e) => {
              previous.value = (e.target as HTMLInputElement).checked;
              loading.value = true;
            }}
          />
          Previous
        </label>

        <label class="flex items-center gap-1.5 text-sm text-slate-600 dark:text-slate-300">
          <input
            type="checkbox"
            checked={autoRefresh.value}
            onChange={(e) => {
              autoRefresh.value = (e.target as HTMLInputElement).checked;
            }}
          />
          Auto-refresh (5s)
        </label>

        <Button
          type="button"
          variant="ghost"
          size="sm"
          onClick={() => {
            loading.value = true;
            fetchLogs();
          }}
        >
          Refresh
        </Button>

        <span class="text-xs text-slate-400">
          {lines.value.length} lines
        </span>
      </div>

      {/* Error */}
      {error.value && (
        <div class="rounded-md bg-red-50 px-3 py-2 text-sm text-red-700 dark:bg-red-900/30 dark:text-red-400">
          {error.value}
        </div>
      )}

      {/* Log output */}
      <pre
        ref={preRef}
        class="h-[600px] overflow-auto rounded-lg bg-slate-900 p-4 font-mono text-xs leading-5 text-slate-200"
      >
        {loading.value && lines.value.length === 0
          ? "Loading logs..."
          : lines.value.length === 0
          ? "No log output"
          : lines.value.join("\n")}
      </pre>
    </div>
  );
}
