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
  /** Init container names (shown with "(init)" label) */
  initContainers?: string[];
}

interface LogResponse {
  lines: string[] | null;
  container: string;
  count: number;
}

interface TabState {
  lines: string[];
  ws: WebSocket | null;
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
  // Per-tab state stored as array signal
  const tabStates = useSignal<TabState[]>(
    containers.map(() => ({
      lines: [],
      ws: null,
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
      const AnsiUp = mod.default || mod;
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
      ws: null,
      droppedCount: 0,
      connected: false,
      error: "",
    };

  const updateTabState = (idx: number, update: Partial<TabState>) => {
    const states = [...tabStates.value];
    states[idx] = { ...states[idx], ...update };
    tabStates.value = states;
  };

  // Connect WS for follow mode
  const connectWS = useCallback((tabIdx: number) => {
    const container = allContainers[tabIdx]?.name;
    if (!container) return;

    // Close existing connection
    const existing = getTabState(tabIdx).ws;
    if (existing) {
      existing.close();
    }

    const proto = globalThis.location.protocol === "https:" ? "wss:" : "ws:";
    const url =
      `${proto}//${globalThis.location.host}/ws/v1/ws/logs/${namespace}/${pod}/${container}`;

    const ws = new WebSocket(url);
    updateTabState(tabIdx, { ws, error: "", connected: false });

    ws.onopen = () => {
      // Send auth
      const token = getAccessToken();
      if (!token) {
        ws.close();
        updateTabState(tabIdx, { error: "Not authenticated", ws: null });
        return;
      }
      ws.send(JSON.stringify({ type: "auth", token }));

      // Send filter
      ws.send(JSON.stringify({
        container,
        tailLines: 500,
        previous: previous.value,
        timestamps: true,
      }));
    };

    ws.onmessage = (event) => {
      try {
        const msg = JSON.parse(event.data);
        if (msg.type === "subscribed") {
          updateTabState(tabIdx, { connected: true });
          loading.value = false;
          return;
        }
        if (msg.type === "log") {
          const state = getTabState(tabIdx);
          const newLines = [...state.lines, msg.data];
          // Trim to MAX_LINES
          const trimmed = newLines.length > MAX_LINES
            ? newLines.slice(newLines.length - MAX_LINES)
            : newLines;
          updateTabState(tabIdx, { lines: trimmed });
          // Auto-scroll
          if (autoScroll.value) {
            requestAnimationFrame(() => {
              const pre = preRefs.current[tabIdx];
              if (pre) pre.scrollTop = pre.scrollHeight;
            });
          }
          return;
        }
        if (msg.type === "dropped") {
          const state = getTabState(tabIdx);
          updateTabState(tabIdx, {
            droppedCount: state.droppedCount + (msg.count ?? 0),
          });
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
      updateTabState(tabIdx, { ws: null, connected: false });
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
      updateTabState(tabIdx, { lines: res.data.lines ?? [] });
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
      // Close any WS connections
      tabStates.value.forEach((state, i) => {
        if (state.ws) {
          state.ws.close();
          updateTabState(i, { ws: null, connected: false });
        }
      });
      fetchSnapshot(idx);
    }

    return () => {
      // Cleanup WS on unmount
      tabStates.value.forEach((state) => {
        if (state.ws) state.ws.close();
      });
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

  // Search filtering
  const getFilteredLines = (tabIdx: number): string[] => {
    const state = getTabState(tabIdx);
    if (!search.value.trim()) return state.lines;
    const q = search.value.toLowerCase();
    return state.lines.filter((line) => line.toLowerCase().includes(q));
  };

  // Render log content with ANSI colors and search highlighting
  const renderLogContent = (tabIdx: number): string => {
    const filteredLines = getFilteredLines(tabIdx);
    if (filteredLines.length === 0) {
      if (loading.value) return "Loading logs...";
      return "No log output";
    }

    const ansi = ansiUpRef.current;
    const q = search.value.trim().toLowerCase();

    return filteredLines.map((line) => {
      let html = ansi ? ansi.ansi_to_html(line) : escapeHtml(line);
      // Highlight search matches
      if (q) {
        html = html.replace(
          new RegExp(escapeRegExp(q), "gi"),
          (match: string) =>
            `<mark class="bg-yellow-300 dark:bg-yellow-700">${match}</mark>`,
        );
      }
      return html;
    }).join("\n");
  };

  // Download logs
  const downloadLogs = (tabIdx: number) => {
    const state = getTabState(tabIdx);
    const content = state.lines.join("\n");
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
        class="h-[600px] overflow-auto rounded-lg bg-slate-900 p-4 font-mono text-xs leading-5 text-slate-200"
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
        <div class="flex items-center gap-1 border-b border-slate-200 dark:border-slate-700">
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
                  : "border-transparent text-slate-500 hover:text-slate-700 dark:hover:text-slate-300"
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
        <div class="flex rounded-md border border-slate-300 dark:border-slate-600">
          <button
            type="button"
            onClick={() => {
              mode.value = "follow";
            }}
            class={`px-3 py-1 text-xs font-medium ${
              mode.value === "follow"
                ? "bg-brand text-white"
                : "text-slate-600 hover:bg-slate-100 dark:text-slate-300 dark:hover:bg-slate-700"
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
                : "text-slate-600 hover:bg-slate-100 dark:text-slate-300 dark:hover:bg-slate-700"
            } rounded-r-md`}
          >
            Snapshot
          </button>
        </div>

        <label class="flex items-center gap-1.5 text-sm text-slate-600 dark:text-slate-300">
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
          <label class="flex items-center gap-1.5 text-sm text-slate-600 dark:text-slate-300">
            <input
              type="checkbox"
              checked={splitView.value}
              onChange={(e) => {
                splitView.value = (e.target as HTMLInputElement).checked;
                if ((e.target as HTMLInputElement).checked) {
                  // Pick second container different from active
                  const other = activeTab.value === 0 ? 1 : 0;
                  splitTab.value = other;
                  if (mode.value === "follow") connectWS(other);
                  else fetchSnapshot(other);
                }
              }}
            />
            Split view
          </label>
        )}

        {mode.value === "snapshot" && (
          <Button
            type="button"
            variant="ghost"
            size="sm"
            onClick={() => fetchSnapshot(activeTab.value)}
          >
            Refresh
          </Button>
        )}

        <Button
          type="button"
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
              currentState.connected
                ? "text-green-600 dark:text-green-400"
                : "text-slate-400"
            }`}
          >
            <span
              class={`inline-block h-2 w-2 rounded-full ${
                currentState.connected
                  ? "bg-green-500 animate-pulse"
                  : "bg-slate-400"
              }`}
            />
            {currentState.connected ? "Streaming" : "Disconnected"}
          </span>
        )}

        <span class="text-xs text-slate-400">
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
          <span class="text-xs text-slate-400">
            {matchCount.value} matching lines
          </span>
        )}
      </div>

      {/* Error */}
      {currentState.error && (
        <div class="rounded-md bg-red-50 px-3 py-2 text-sm text-red-700 dark:bg-red-900/30 dark:text-red-400">
          {currentState.error}
        </div>
      )}

      {/* Log output */}
      {splitView.value
        ? (
          <div class="grid grid-cols-2 gap-2">
            <div>
              <div class="mb-1 text-xs font-medium text-slate-500">
                {allContainers[activeTab.value]?.name}
              </div>
              {renderLogPanel(activeTab.value)}
            </div>
            <div>
              <div class="mb-1 text-xs font-medium text-slate-500">
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
