/** @jsxImportSource react */
import { useEffect, useState } from "react";
import { Button } from "@/components/ui/Button.tsx";
import { apiGet } from "@/lib/api.ts";
import { IS_BROWSER } from "@/src/lib/is-browser.ts";
import { selectedNamespace } from "@/src/lib/namespace.ts";
import { useIsClient } from "@/src/lib/use-is-client.ts";

interface LogFilterBarProps {
  onSearch: (
    query: string,
    start: string,
    end: string,
    limit: number,
    direction: string,
  ) => void;
  onLiveTail: (query: string, namespace: string) => void;
  onStopTail: () => void;
  isTailing: boolean;
  loading: boolean;
}

export default function LogFilterBar(props: LogFilterBarProps) {
  const isClient = useIsClient();
  const [mode, setMode] = useState<"search" | "logql">("search");
  const [searchText, setSearchText] = useState("");
  const [namespace, setNamespace] = useState("");
  const [pod, setPod] = useState("");
  const [container, setContainer] = useState("");
  const [severity, setSeverity] = useState("");
  const [timePreset, setTimePreset] = useState("1h");
  const [namespaces, setNamespaces] = useState<string[]>([]);
  const [pods, setPods] = useState<string[]>([]);
  const [containers, setContainers] = useState<string[]>([]);

  // Start from the global namespace selection. Its "all namespaces" value is
  // the sentinel "all", which this bar spells as "" (the All Namespaces option).
  useEffect(() => {
    if (!IS_BROWSER) return;
    const ns = selectedNamespace.value;
    if (ns && ns !== "all") {
      setNamespace(ns);
    }
  }, []);

  // Fetch available namespaces from Loki labels
  useEffect(() => {
    if (!IS_BROWSER) return;
    apiGet<string[]>("/v1/logs/labels/namespace/values")
      .then((res) => {
        setNamespaces(res.data ?? []);
      })
      .catch(() => {}); // graceful — dropdowns just stay empty
  }, []);

  // Fetch pods when namespace changes
  useEffect(() => {
    if (!IS_BROWSER || !namespace) return;
    const query = `{namespace="${namespace}"}`;
    apiGet<string[]>(
      `/v1/logs/labels/pod/values?query=${encodeURIComponent(
        query,
      )}&namespace=${encodeURIComponent(namespace)}`,
    )
      .then((res) => {
        setPods(res.data ?? []);
      })
      .catch(() => {});
  }, [namespace]);

  // Fetch containers when pod changes. A namespace change clears the pod in
  // the same update, so this never runs for a namespace alone.
  useEffect(() => {
    if (!IS_BROWSER || !namespace || !pod) return;
    const query = `{namespace="${namespace}",pod="${pod}"}`;
    apiGet<string[]>(
      `/v1/logs/labels/container/values?query=${encodeURIComponent(
        query,
      )}&namespace=${encodeURIComponent(namespace)}`,
    )
      .then((res) => {
        setContainers(res.data ?? []);
      })
      .catch(() => {});
  }, [namespace, pod]);

  function buildQuery(): string {
    if (mode === "logql") return searchText;

    const matchers: string[] = [];
    if (namespace) matchers.push(`namespace="${namespace}"`);
    if (pod) matchers.push(`pod=~"${pod}.*"`);
    if (container) matchers.push(`container="${container}"`);
    // Loki rejects an empty selector, so "everything" needs a matcher that
    // matches every stream with a namespace label.
    if (matchers.length === 0) matchers.push(`namespace=~".+"`);

    let q = `{${matchers.join(",")}}`;
    if (severity) q += ` | level=~"(?i)${severity}"`;
    if (searchText && mode === "search") {
      q += ` |= "${searchText}"`;
    }
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
    const ms = presets[timePreset] ?? presets["1h"];
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
    if (props.isTailing) {
      props.onStopTail();
    } else {
      props.onLiveTail(buildQuery(), namespace);
    }
  }

  if (!isClient) return null;

  return (
    <div className="flex flex-wrap items-center gap-2 rounded-lg border border-border-primary bg-surface p-3">
      <select
        className="rounded border border-border-primary bg-elevated px-2 py-1.5 text-sm text-text-secondary"
        value={namespace}
        onChange={(e) => {
          setNamespace(e.currentTarget.value);
          setPod("");
          setContainer("");
        }}
      >
        <option value="">All Namespaces</option>
        {namespaces.map((ns) => (
          <option key={ns} value={ns}>
            {ns}
          </option>
        ))}
      </select>

      <select
        className="rounded border border-border-primary bg-elevated px-2 py-1.5 text-sm text-text-secondary"
        value={pod}
        onChange={(e) => {
          setPod(e.currentTarget.value);
          setContainer("");
        }}
      >
        <option value="">All Pods</option>
        {pods.map((p) => (
          <option key={p} value={p}>
            {p}
          </option>
        ))}
      </select>

      <select
        className="rounded border border-border-primary bg-elevated px-2 py-1.5 text-sm text-text-secondary"
        value={container}
        onChange={(e) => {
          setContainer(e.currentTarget.value);
        }}
      >
        <option value="">All Containers</option>
        {containers.map((c) => (
          <option key={c} value={c}>
            {c}
          </option>
        ))}
      </select>

      <select
        className="rounded border border-border-primary bg-elevated px-2 py-1.5 text-sm text-text-secondary"
        value={severity}
        onChange={(e) => {
          setSeverity(e.currentTarget.value);
        }}
      >
        <option value="">All Levels</option>
        <option value="error">Error</option>
        <option value="warn">Warning</option>
        <option value="info">Info</option>
        <option value="debug">Debug</option>
      </select>

      <div className="flex flex-1 items-center gap-1">
        <input
          type="text"
          className="flex-1 rounded border border-border-primary bg-elevated px-2 py-1.5 text-sm text-text-primary placeholder-text-muted"
          placeholder={mode === "logql" ? "LogQL query..." : "Search text..."}
          value={searchText}
          onChange={(e) => {
            setSearchText(e.currentTarget.value);
          }}
          onKeyDown={(e) => {
            if (e.key === "Enter") handleSearch();
          }}
        />
        <button
          type="button"
          className="rounded border border-border-primary bg-elevated px-2 py-1.5 text-xs text-text-muted hover:text-text-secondary"
          onClick={() => {
            setMode(mode === "search" ? "logql" : "search");
          }}
          title={
            mode === "search" ? "Switch to LogQL mode" : "Switch to search mode"
          }
        >
          {mode === "search" ? "LogQL" : "Search"}
        </button>
      </div>

      <select
        className="rounded border border-border-primary bg-elevated px-2 py-1.5 text-sm text-text-secondary"
        value={timePreset}
        onChange={(e) => {
          setTimePreset(e.currentTarget.value);
        }}
      >
        <option value="15m">Last 15m</option>
        <option value="1h">Last 1h</option>
        <option value="6h">Last 6h</option>
        <option value="24h">Last 24h</option>
        <option value="7d">Last 7d</option>
      </select>

      <Button onClick={handleSearch} disabled={props.loading}>
        Run
      </Button>
      <button
        type="button"
        className={`rounded px-3 py-1.5 text-sm font-semibold ${
          props.isTailing
            ? "bg-danger"
            : "border border-accent bg-elevated text-accent"
        }`}
        style={props.isTailing ? { color: "var(--bg-base)" } : undefined}
        onClick={handleLiveTail}
      >
        {props.isTailing ? "Stop" : "Live Tail"}
      </button>
    </div>
  );
}
