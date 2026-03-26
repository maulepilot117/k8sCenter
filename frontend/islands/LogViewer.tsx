import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { useCallback, useEffect, useRef } from "preact/hooks";
import { apiGet, getAccessToken } from "@/lib/api.ts";
import { Button } from "@/components/ui/Button.tsx";
import { SearchBar } from "@/components/ui/SearchBar.tsx";

interface LogViewerProps {
  namespace: string;
  pod: string;
  containers: string[];
  /** Init container names (shown with"(init)" label) */
  initContainers?: string[];
}

interface LogResponse {
  lines: string[] | null;
  container: string;
  count: number;
}

interface LogLine {
  raw: string;
  html: string; // pre-computed ANSI HTML (cached at ingestion time)
}

interface TabState {
  lines: LogLine[];
  droppedCount: number;
  connected: boolean;
  error: string;
}

const MAX_LINES = 10_000;

export default function LogViewer(
  { namespace, pod, containers, initContainers = [] }: LogViewerProps,
) {
  const activeTab = useSignal(0);
  const mode = useSignal<"follow" | "snapshot">("follow");
  const search = useSignal("");
  const previous = useSignal(false);
  const splitView = useSignal(false);
  const splitTab = useSignal(1); // second tab in split view
  // Per-tab state stored as array signal (display-relevant data only)
  const tabStates = useSignal<TabState[]>(
    containers.map(() => ({
      lines: [],
      droppedCount: 0,
      connected: false,
      error: "",
    })),
  );
  const loading = useSignal(true);
  const autoScroll = useSignal(true);
  const preRefs = useRef<(HTMLPreElement | null)[]>([]);
  // deno-lint-ignore no-explicit-any -- ansi_up has no TypeScript types in npm package
  const ansiUpRef = useRef<any>(null);

  // WS refs — tracked separately from signal to avoid storing non-serializable objects in signals
  const wsRefs = useRef<Map<number, WebSocket>>(new Map());

  // RAF batching — accumulate incoming lines, flush once per animation frame
  const pendingLines = useRef<Map<number, string[]>>(new Map());
  const pendingDrops = useRef<Map<number, number>>(new Map());
  const flushRafId = useRef(0);
  const scrollRafId = useRef(0);

  const allContainers = [
    ...containers.map((c) => ({
      name: c,
      init: false,
    })),
    ...initContainers.map((c) => ({
      name: c,
      init: true,
    })),
  ];

  // Load ansi_up dynamically (browser-only)
  useEffect(() => {
    if (!IS_BROWSER) return;
    import("ansi_up").then((mod) => {
      // deno-lint-ignore no-explicit-any -- ansi_up export varies by bundler
      const AnsiUp = (mod as any).default ?? mod;
      const instance = new AnsiUp();
      instance.use_classes = true;
      instance.escape_html = true;
      ansiUpRef.current = instance;
    });
  }, []);

  // Get current tab state
  const getTabState = (idx: number): TabState =>
    tabStates.value[idx] ?? {
      lines: [],
      droppedCount: 0,
      connected: false,
      error: "",
    };

  const updateTabState = (idx: number, update: Partial<TabState>) => {
    const states = [...tabStates.value];
    states[idx] = { ...states[idx], ...update };
    tabStates.value = states;
  };

  // Convert a raw log line to a LogLine with cached HTML
  const toLogLine = (raw: string): LogLine => {
    const ansi = ansiUpRef.current;
    return {
      raw,
      html: ansi ? ansi.ansi_to_html(raw) : escapeHtml(raw),
    };
  };

  // Flush pending log lines from buffer into signal (called once per RAF)
  const flushPendingLines = () => {
    flushRafId.current = 0;
    let changed = false;

    const states = [...tabStates.value];
    for (const [idx, rawLines] of pendingLines.current) {
      if (rawLines.length === 0) continue;
      const logLines = rawLines.map(toLogLine);
      const merged = [...(states[idx]?.lines ?? []), ...logLines];
      const trimmed = merged.length > MAX_LINES
        ? merged.slice(merged.length - MAX_LINES)
        : merged;
      states[idx] = { ...states[idx], lines: trimmed };
      changed = true;
    }
    pendingLines.current.clear();

    for (const [idx, count] of pendingDrops.current) {
      if (count > 0) {
        states[idx] = {
          ...states[idx],
          droppedCount: (states[idx]?.droppedCount ?? 0) + count,
        };
        changed = true;
      }
    }
    pendingDrops.current.clear();

    if (changed) {
      tabStates.value = states;
      // Auto-scroll after batch flush
      if (autoScroll.value && !scrollRafId.current) {
        scrollRafId.current = requestAnimationFrame(() => {
          scrollRafId.current = 0;
          const pre = preRefs.current[activeTab.value];
          if (pre) pre.scrollTop = pre.scrollHeight;
        });
      }
    }
  };

  // Close a specific tab's WS connection
  const closeTabWS = (tabIdx: number) => {
    const ws = wsRefs.current.get(tabIdx);
    if (ws) {
      ws.close();
      wsRefs.current.delete(tabIdx);
    }
  };

  // Connect WS for follow mode — returns the WS for cleanup tracking
  const connectWS = useCallback((tabIdx: number) => {
    const container = allContainers[tabIdx]?.name;
    if (!container) return;

    // Close existing connection for this tab
    closeTabWS(tabIdx);

    const proto = globalThis.location.protocol === "https:" ? "wss:" : "ws:";
    const url =
      `${proto}//${globalThis.location.host}/ws/v1/ws/logs/${namespace}/${pod}/${container}`;

    const ws = new WebSocket(url);
    wsRefs.current.set(tabIdx, ws);
    updateTabState(tabIdx, { error: "", connected: false });

    ws.onopen = () => {
      const token = getAccessToken();
      if (!token) {
        ws.close();
        wsRefs.current.delete(tabIdx);
        updateTabState(tabIdx, { error: "Not authenticated" });
        return;
      }
      ws.send(JSON.stringify({ type: "auth", token }));
      ws.send(JSON.stringify({
        container,
        tailLines: 500,
        previous: previous.value,
        timestamps: true,
      }));
    };

    ws.onmessage = (event) => {
      // Mode guard: discard messages if we've switched to snapshot mode
      if (mode.value !== "follow") return;

      try {
        const msg = JSON.parse(event.data);
        if (msg.type === "subscribed") {
          updateTabState(tabIdx, { connected: true });
          loading.value = false;
          return;
        }
        if (msg.type === "log") {
          // Batch into pending buffer — flushed once per RAF
          const pending = pendingLines.current.get(tabIdx) ?? [];
          pending.push(msg.data);
          pendingLines.current.set(tabIdx, pending);
          if (!flushRafId.current) {
            flushRafId.current = requestAnimationFrame(flushPendingLines);
          }
          return;
        }
        if (msg.type === "dropped") {
          const current = pendingDrops.current.get(tabIdx) ?? 0;
          pendingDrops.current.set(tabIdx, current + (msg.count ?? 0));
          if (!flushRafId.current) {
            flushRafId.current = requestAnimationFrame(flushPendingLines);
          }
          return;
        }
        if (msg.type === "error") {
          updateTabState(tabIdx, { error: msg.message });
          return;
        }
        if (msg.type === "end") {
          updateTabState(tabIdx, { connected: false });
          return;
        }
      } catch {
        // ignore unparseable
      }
    };

    ws.onclose = () => {
      wsRefs.current.delete(tabIdx);
      updateTabState(tabIdx, { connected: false });
    };

    ws.onerror = () => {
      updateTabState(tabIdx, { error: "WebSocket connection failed" });
    };
  }, [namespace, pod, previous.value]);

  // Fetch logs via HTTP (snapshot mode)
  const fetchSnapshot = useCallback(async (tabIdx: number) => {
    const container = allContainers[tabIdx]?.name;
    if (!container) return;
    loading.value = true;
    updateTabState(tabIdx, { error: "" });
    try {
      const params = new URLSearchParams({
        container,
        tailLines: "500",
        previous: String(previous.value),
        timestamps: "true",
      });
      const res = await apiGet<LogResponse>(
        `/v1/resources/pods/${namespace}/${pod}/logs?${params}`,
      );
      updateTabState(tabIdx, {
        lines: (res.data.lines ?? []).map(toLogLine),
      });
      loading.value = false;
      // Scroll to bottom
      requestAnimationFrame(() => {
        const pre = preRefs.current[tabIdx];
        if (pre) pre.scrollTop = pre.scrollHeight;
      });
    } catch (err) {
      updateTabState(tabIdx, {
        error: err instanceof Error ? err.message : "Failed to fetch logs",
      });
      loading.value = false;
    }
  }, [namespace, pod, previous.value]);

  // Connect/fetch on mode or tab change
  useEffect(() => {
    if (!IS_BROWSER) return;

    const idx = activeTab.value;

    if (mode.value === "follow") {
      connectWS(idx);
      if (splitView.value && splitTab.value !== idx) {
        connectWS(splitTab.value);
      }
    } else {
      // Close ALL WS connections when switching to snapshot mode
      for (const [i, ws] of wsRefs.current) {
        ws.close();
        wsRefs.current.delete(i);
        updateTabState(i, { connected: false });
      }
      fetchSnapshot(idx);
    }

    return () => {
      // Cleanup all WS on unmount or dependency change
      for (const [, ws] of wsRefs.current) {
        ws.close();
      }
      wsRefs.current.clear();
      // Cancel pending RAFs
      if (flushRafId.current) cancelAnimationFrame(flushRafId.current);
      if (scrollRafId.current) cancelAnimationFrame(scrollRafId.current);
      flushRafId.current = 0;
      scrollRafId.current = 0;
      pendingLines.current.clear();
      pendingDrops.current.clear();
    };
  }, [activeTab.value, mode.value, previous.value]);

  // Scroll detection for auto-scroll pause
  const handleScroll = (tabIdx: number) => {
    const pre = preRefs.current[tabIdx];
    if (!pre) return;
    const atBottom = pre.scrollTop + pre.clientHeight >= pre.scrollHeight - 20;
    if (!atBottom && autoScroll.value) {
      autoScroll.value = false;
    }
  };

  // Search filtering — operates on raw text, returns LogLine[]
  const getFilteredLines = (tabIdx: number): LogLine[] => {
    const state = getTabState(tabIdx);
    if (!search.value.trim()) return state.lines;
    const q = search.value.toLowerCase();
    return state.lines.filter((line) => line.raw.toLowerCase().includes(q));
  };

  // Render log content using pre-cached HTML + search highlighting on raw text
  const renderLogContent = (tabIdx: number): string => {
    const filteredLines = getFilteredLines(tabIdx);
    if (filteredLines.length === 0) {
      if (loading.value) return "Loading logs...";
      return "No log output";
    }

    const q = search.value.trim().toLowerCase();

    if (!q) {
      // Fast path: no search, just join pre-computed HTML
      return filteredLines.map((l) => l.html).join("\n");
    }

    // Search highlighting: apply on the pre-computed HTML but only on text
    // content (skip inside HTML tags to avoid breaking <span class="...">)
    const re = new RegExp(escapeRegExp(q), "gi");
    return filteredLines.map((l) => {
      return l.html.replace(
        /(<[^>]*>)|([^<]+)/g,
        (_full: string, tag: string, text: string) => {
          if (tag) return tag; // pass HTML tags through unchanged
          return text.replace(
            re,
            (match: string) =>
              `<mark class="bg-warning-dim text-text-primary">${match}</mark>`,
          );
        },
      );
    }).join("\n");
  };

  // Download logs
  const downloadLogs = (tabIdx: number) => {
    const state = getTabState(tabIdx);
    const content = state.lines.map((l) => l.raw).join("\n");
    const blob = new Blob([content], { type: "text/plain" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = `${pod}-${allContainers[tabIdx]?.name ?? "unknown"}-${
      new Date().toISOString().replace(/[:.]/g, "-")
    }.log`;
    a.click();
    URL.revokeObjectURL(url);
  };

  if (!IS_BROWSER) return null;

  const currentState = getTabState(activeTab.value);
  // Compute match count inline (avoid conditional hook call)
  const matchCount = search.value.trim()
    ? getFilteredLines(activeTab.value).length
    : 0;

  const renderLogPanel = (tabIdx: number) => {
    return (
      <pre
        ref={(el) => {
          preRefs.current[tabIdx] = el;
        }}
        class="h-[600px] overflow-auto rounded-lg bg-base p-4 font-mono text-xs leading-5 text-text-primary"
        onScroll={() => handleScroll(tabIdx)}
        // deno-lint-ignore react-no-danger -- ANSI color rendering requires innerHTML; input is escaped by ansi_up
        dangerouslySetInnerHTML={{ __html: renderLogContent(tabIdx) }}
      />
    );
  };

  return (
    <div class="flex flex-col gap-3">
      {/* Container tabs */}
      {allContainers.length > 1 && (
        <div class="flex items-center gap-1 border-b border-border-primary">
          {allContainers.map((c, idx) => (
            <button
              key={c.name}
              type="button"
              onClick={() => {
                activeTab.value = idx;
              }}
              class={`px-3 py-1.5 text-sm font-medium border-b-2 transition-colors ${
                activeTab.value === idx
                  ? "border-brand text-brand"
                  : "border-transparent text-text-muted hover:text-text-primary"
              }`}
            >
              {c.name}
              {c.init ? " (init)" : ""}
            </button>
          ))}
        </div>
      )}

      {/* Controls */}
      <div class="flex flex-wrap items-center gap-3">
        {/* Mode toggle */}
        <div class="flex rounded-md border border-border-primary">
          <button
            type="button"
            onClick={() => {
              mode.value = "follow";
            }}
            class={`px-3 py-1 text-xs font-medium ${
              mode.value === "follow"
                ? "bg-brand text-white"
                : "text-text-secondary hover:bg-elevated text-text-secondary"
            } rounded-l-md`}
          >
            Follow
          </button>
          <button
            type="button"
            onClick={() => {
              mode.value = "snapshot";
            }}
            class={`px-3 py-1 text-xs font-medium ${
              mode.value === "snapshot"
                ? "bg-brand text-white"
                : "text-text-secondary hover:bg-elevated text-text-secondary"
            } rounded-r-md`}
          >
            Snapshot
          </button>
        </div>

        <label class="flex items-center gap-1.5 text-sm text-text-secondary">
          <input
            type="checkbox"
            checked={previous.value}
            onChange={(e) => {
              previous.value = (e.target as HTMLInputElement).checked;
            }}
          />
          Previous
        </label>

        {/* Split view (only for multi-container pods) */}
        {allContainers.length >= 2 && (
          <label class="flex items-center gap-1.5 text-sm text-text-secondary">
            <input
              type="checkbox"
              checked={splitView.value}
              onChange={(e) => {
                const checked = (e.target as HTMLInputElement).checked;
                if (checked) {
                  // Pick second container different from active
                  const other = activeTab.value === 0 ? 1 : 0;
                  splitTab.value = other;
                  if (mode.value === "follow") connectWS(other);
                  else fetchSnapshot(other);
                } else {
                  // Close the split tab's WS when disabling split view
                  closeTabWS(splitTab.value);
                  updateTabState(splitTab.value, { connected: false });
                }
                splitView.value = checked;
              }}
            />
            Split view
          </label>
        )}

        {mode.value === "snapshot" && (
          <Button
            variant="ghost"
            size="sm"
            onClick={() => fetchSnapshot(activeTab.value)}
          >
            Refresh
          </Button>
        )}

        <Button
          variant="ghost"
          size="sm"
          onClick={() => downloadLogs(activeTab.value)}
        >
          Download
        </Button>

        {/* Status indicators */}
        {mode.value === "follow" && (
          <span
            class={`inline-flex items-center gap-1 text-xs ${
              currentState.connected ? "text-success" : "text-text-muted"
            }`}
          >
            <span
              class={`inline-block h-2 w-2 rounded-full ${
                currentState.connected
                  ? "bg-green-500 animate-pulse"
                  : "bg-text-muted"
              }`}
            />
            {currentState.connected ? "Streaming" : "Disconnected"}
          </span>
        )}

        <span class="text-xs text-text-muted">
          {getTabState(activeTab.value).lines.length} lines
          {currentState.droppedCount > 0 &&
            ` (${currentState.droppedCount} dropped)`}
        </span>
      </div>

      {/* Search */}
      <div class="flex items-center gap-2">
        <div class="max-w-sm flex-1">
          <SearchBar
            value={search.value}
            onInput={(v) => {
              search.value = v;
            }}
            placeholder="Search logs..."
          />
        </div>
        {search.value && (
          <span class="text-xs text-text-muted">
            {matchCount} matching lines
          </span>
        )}
      </div>

      {/* Error */}
      {currentState.error && (
        <div class="rounded-md bg-danger-dim px-3 py-2 text-sm text-red-700 bg-danger-dim text-danger">
          {currentState.error}
        </div>
      )}

      {/* Log output */}
      {splitView.value
        ? (
          <div class="grid grid-cols-2 gap-2">
            <div>
              <div class="mb-1 text-xs font-medium text-text-muted">
                {allContainers[activeTab.value]?.name}
              </div>
              {renderLogPanel(activeTab.value)}
            </div>
            <div>
              <div class="mb-1 text-xs font-medium text-text-muted">
                {allContainers[splitTab.value]?.name}
              </div>
              {renderLogPanel(splitTab.value)}
            </div>
          </div>
        )
        : renderLogPanel(activeTab.value)}

      {/* Resume auto-scroll FAB */}
      {!autoScroll.value && mode.value === "follow" && (
        <button
          type="button"
          onClick={() => {
            autoScroll.value = true;
            const pre = preRefs.current[activeTab.value];
            if (pre) pre.scrollTop = pre.scrollHeight;
          }}
          class="fixed bottom-6 right-6 z-30 rounded-full bg-brand px-4 py-2 text-sm font-medium text-white shadow-lg hover:bg-brand/90"
        >
          Resume auto-scroll
        </button>
      )}
    </div>
  );
}

function escapeHtml(s: string): string {
  return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}

function escapeRegExp(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}
