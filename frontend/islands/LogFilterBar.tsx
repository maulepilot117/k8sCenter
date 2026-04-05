import { useSignal, type Signal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiGet } from "@/lib/api.ts";
import { selectedNamespace } from "@/lib/namespace.ts";
import { Button } from "@/components/ui/Button.tsx";

interface LogFilterBarProps {
  onSearch: (query: string, start: string, end: string, limit: number, direction: string) => void;
  onLiveTail: (query: string, namespace: string) => void;
  onStopTail: () => void;
  isTailing: Signal<boolean>;
  loading: Signal<boolean>;
}

export default function LogFilterBar(props: LogFilterBarProps) {
  const mode = useSignal<"search" | "logql">("search");
  const searchText = useSignal("");
  const namespace = useSignal("");
  const pod = useSignal("");
  const container = useSignal("");
  const severity = useSignal("");
  const timePreset = useSignal("1h");
  const namespaces = useSignal<string[]>([]);
  const pods = useSignal<string[]>([]);
  const containers = useSignal<string[]>([]);

  // Sync with global namespace selection
  useEffect(() => {
    if (!IS_BROWSER) return;
    if (selectedNamespace.value) {
      namespace.value = selectedNamespace.value;
    }
  }, []);

  // Fetch available namespaces from Loki labels
  useEffect(() => {
    if (!IS_BROWSER) return;
    apiGet<string[]>("/v1/logs/labels/namespace/values")
      .then((res) => { namespaces.value = res.data ?? []; })
      .catch(() => {}); // graceful — dropdowns just stay empty
  }, []);

  // Fetch pods when namespace changes
  useEffect(() => {
    if (!IS_BROWSER || !namespace.value) return;
    const query = `{namespace="${namespace.value}"}`;
    apiGet<string[]>(`/v1/logs/labels/pod/values?query=${encodeURIComponent(query)}&namespace=${encodeURIComponent(namespace.value)}`)
      .then((res) => { pods.value = res.data ?? []; })
      .catch(() => {});
  }, [namespace.value]);

  // Fetch containers when pod changes
  useEffect(() => {
    if (!IS_BROWSER || !namespace.value || !pod.value) return;
    const query = `{namespace="${namespace.value}",pod="${pod.value}"}`;
    apiGet<string[]>(`/v1/logs/labels/container/values?query=${encodeURIComponent(query)}&namespace=${encodeURIComponent(namespace.value)}`)
      .then((res) => { containers.value = res.data ?? []; })
      .catch(() => {});
  }, [pod.value]);

  function buildQuery(): string {
    if (mode.value === "logql") return searchText.value;

    const matchers: string[] = [];
    if (namespace.value) matchers.push(`namespace="${namespace.value}"`);
    if (pod.value) matchers.push(`pod=~"${pod.value}.*"`);
    if (container.value) matchers.push(`container="${container.value}"`);

    let q = `{${matchers.join(",")}}`;
    if (severity.value) q += ` | level=~"(?i)${severity.value}"`;
    if (searchText.value && mode.value === "search") q += ` |= "${searchText.value}"`;
    return q;
  }

  function getTimeRange(): { start: string; end: string } {
    const now = new Date();
    const presets: Record<string, number> = {
      "15m": 15 * 60 * 1000,
      "1h": 60 * 60 * 1000,
      "6h": 6 * 60 * 60 * 1000,
      "24h": 24 * 60 * 60 * 1000,
      "7d": 7 * 24 * 60 * 60 * 1000,
    };
    const ms = presets[timePreset.value] ?? presets["1h"];
    return {
      start: new Date(now.getTime() - ms).toISOString(),
      end: now.toISOString(),
    };
  }

  function handleSearch() {
    const { start, end } = getTimeRange();
    props.onSearch(buildQuery(), start, end, 1000, "backward");
  }

  function handleLiveTail() {
    if (props.isTailing.value) {
      props.onStopTail();
    } else {
      props.onLiveTail(buildQuery(), namespace.value);
    }
  }

  if (!IS_BROWSER) return null;

  return (
    <div class="flex flex-wrap items-center gap-2 rounded-lg border border-border-primary bg-bg-surface p-3">
      <select
        class="rounded border border-border-primary bg-bg-elevated px-2 py-1.5 text-sm text-text-secondary"
        value={namespace.value}
        onChange={(e) => { namespace.value = (e.target as HTMLSelectElement).value; pod.value = ""; container.value = ""; }}
      >
        <option value="">All Namespaces</option>
        {namespaces.value.map((ns) => <option key={ns} value={ns}>{ns}</option>)}
      </select>

      <select
        class="rounded border border-border-primary bg-bg-elevated px-2 py-1.5 text-sm text-text-secondary"
        value={pod.value}
        onChange={(e) => { pod.value = (e.target as HTMLSelectElement).value; container.value = ""; }}
      >
        <option value="">All Pods</option>
        {pods.value.map((p) => <option key={p} value={p}>{p}</option>)}
      </select>

      <select
        class="rounded border border-border-primary bg-bg-elevated px-2 py-1.5 text-sm text-text-secondary"
        value={container.value}
        onChange={(e) => { container.value = (e.target as HTMLSelectElement).value; }}
      >
        <option value="">All Containers</option>
        {containers.value.map((c) => <option key={c} value={c}>{c}</option>)}
      </select>

      <select
        class="rounded border border-border-primary bg-bg-elevated px-2 py-1.5 text-sm text-text-secondary"
        value={severity.value}
        onChange={(e) => { severity.value = (e.target as HTMLSelectElement).value; }}
      >
        <option value="">All Levels</option>
        <option value="error">Error</option>
        <option value="warn">Warning</option>
        <option value="info">Info</option>
        <option value="debug">Debug</option>
      </select>

      <div class="flex flex-1 items-center gap-1">
        <input
          type="text"
          class="flex-1 rounded border border-border-primary bg-bg-elevated px-2 py-1.5 text-sm text-text-primary placeholder-text-muted"
          placeholder={mode.value === "logql" ? 'LogQL query...' : 'Search text...'}
          value={searchText.value}
          onInput={(e) => { searchText.value = (e.target as HTMLInputElement).value; }}
          onKeyDown={(e) => { if (e.key === "Enter") handleSearch(); }}
        />
        <button
          type="button"
          class="rounded border border-border-primary bg-bg-elevated px-2 py-1.5 text-xs text-text-muted hover:text-text-secondary"
          onClick={() => { mode.value = mode.value === "search" ? "logql" : "search"; }}
          title={mode.value === "search" ? "Switch to LogQL mode" : "Switch to search mode"}
        >
          {mode.value === "search" ? "LogQL" : "Search"}
        </button>
      </div>

      <select
        class="rounded border border-border-primary bg-bg-elevated px-2 py-1.5 text-sm text-text-secondary"
        value={timePreset.value}
        onChange={(e) => { timePreset.value = (e.target as HTMLSelectElement).value; }}
      >
        <option value="15m">Last 15m</option>
        <option value="1h">Last 1h</option>
        <option value="6h">Last 6h</option>
        <option value="24h">Last 24h</option>
        <option value="7d">Last 7d</option>
      </select>

      <Button onClick={handleSearch} disabled={props.loading.value}>
        Run
      </Button>
      <button
        type="button"
        class={`rounded px-3 py-1.5 text-sm font-semibold ${
          props.isTailing.value
            ? "bg-status-error text-white"
            : "border border-accent-primary bg-bg-elevated text-accent-primary"
        }`}
        onClick={handleLiveTail}
      >
        {props.isTailing.value ? "Stop" : "Live Tail"}
      </button>
    </div>
  );
}
