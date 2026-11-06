import { useSignal } from "@preact/signals";
import { useCallback, useEffect, useRef } from "preact/hooks";
import { Button } from "@/components/ui/Button.tsx";
import { ErrorBanner } from "@/components/ui/ErrorBanner.tsx";
import { SearchBar } from "@/components/ui/SearchBar.tsx";
import { ApiError, apiGet, getAccessToken, refreshSession } from "@/lib/api.ts";
import {
  isReplayedLine,
  logLineTimestamp,
  RECONNECT_DELAYS_MS,
  type StreamErrorFrame,
  streamAuthFrame,
  streamErrorMessage,
} from "@/lib/stream-state.ts";
import { SessionClusterBadge } from "@/src/components/SessionClusterBadge.tsx";
import { type ClusterTarget, currentTarget } from "@/src/lib/cluster.ts";
import { IS_BROWSER } from "@/src/lib/is-browser.ts";

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
  /** Waiting out a backoff before resuming after a retryable end. */
  reconnecting: boolean;
  error: string;
}

const MAX_LINES = 10_000;

/** Where a resumed tail rejoins the lines already shown. */
const RECONNECTED_MARKER: LogLine = {
  raw: "--- reconnected ---",
  html: '<span class="text-text-muted">--- reconnected ---</span>',
};

const EMPTY_TAB: TabState = {
  lines: [],
  droppedCount: 0,
  connected: false,
  reconnecting: false,
  error: "",
};

export default function LogViewer({
  namespace,
  pod,
  containers,
  initContainers = [],
}: LogViewerProps) {
  const activeTab = useSignal(0);
  const mode = useSignal<"follow" | "snapshot">("follow");
  const search = useSignal("");
  const previous = useSignal(false);
  const splitView = useSignal(false);
  const splitTab = useSignal(1); // second tab in split view
  // Per-tab state stored as array signal (display-relevant data only)
  const tabStates = useSignal<TabState[]>(containers.map(() => EMPTY_TAB));
  const loading = useSignal(true);
  const autoScroll = useSignal(true);
  const preRefs = useRef<(HTMLPreElement | null)[]>([]);
  // ansi_up ships no TypeScript types in its npm package.
  const ansiUpRef = useRef<any>(null);

  // WS refs — tracked separately from signal to avoid storing non-serializable objects in signals
  const wsRefs = useRef<Map<number, WebSocket>>(new Map());
  // The cluster this viewer streams from, taken once when it opens and kept
  // across every reconnect, so a resume never follows a changed selection
  // (KTD1).
  const pinRef = useRef<ClusterTarget | null>(null);
  if (IS_BROWSER && pinRef.current === null) pinRef.current = currentTarget();

  // Per-tab resume state. A retryable end reconnects after a backoff; the
  // resumed tail replays its last lines, so the ones at or before the last
  // timestamp shown are skipped and one marker shows the join.
  const triesRef = useRef<Map<number, number>>(new Map());
  const retryTimers = useRef<Map<number, ReturnType<typeof setTimeout>>>(
    new Map(),
  );
  // Bumped whenever a tab's pending resume is cancelled. A resume that
  // awaited a session refresh checks it before connecting, so one that
  // outlived its tab, its mode or the viewer opens nothing.
  const resumeEpoch = useRef<Map<number, number>>(new Map());
  const lastTimestamp = useRef<Map<number, [string, number]>>(new Map());
  const resumeAfter = useRef<Map<number, [string, number] | null>>(new Map());

  // RAF batching — accumulate incoming lines, flush once per animation frame
  const pendingLines = useRef<Map<number, (string | LogLine)[]>>(new Map());
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
      // U13 fix: verified live (Astro/Vite/Rollup) that this package's ESM
      // build has no default export at all -- it exports only the named
      // `AnsiUp` class, so the Fresh-era `mod.default ?? mod` fallback
      // silently picked the whole module namespace object (not a
      // constructor) and threw on `new AnsiUp()`. Deno's bundler must have
      // synthesized a default from the CJS build; Astro's does not. Prefer
      // the named export, keep both old fallbacks for whichever shape a
      // future bundler/version produces.
      const AnsiUp = (mod as any).AnsiUp ?? (mod as any).default ?? mod;
      const instance = new AnsiUp();
      instance.use_classes = true;
      instance.escape_html = true;
      ansiUpRef.current = instance;
    });
  }, []);

  // Get current tab state
  const getTabState = (idx: number): TabState =>
    tabStates.value[idx] ?? EMPTY_TAB;

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
      const logLines = rawLines.map((l) =>
        typeof l === "string" ? toLogLine(l) : l,
      );
      const merged = [...(states[idx]?.lines ?? []), ...logLines];
      const trimmed =
        merged.length > MAX_LINES
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

  // Cancel a tab's pending resume, including one already past its timer and
  // waiting on a session refresh.
  const cancelResume = (tabIdx: number) => {
    resumeEpoch.current.set(tabIdx, (resumeEpoch.current.get(tabIdx) ?? 0) + 1);
    const timer = retryTimers.current.get(tabIdx);
    if (timer) {
      clearTimeout(timer);
      retryTimers.current.delete(tabIdx);
    }
  };

  // Close a specific tab's WS connection and cancel a pending resume
  const closeTabWS = (tabIdx: number) => {
    cancelResume(tabIdx);
    const ws = wsRefs.current.get(tabIdx);
    if (ws) {
      ws.close();
      wsRefs.current.delete(tabIdx);
    }
  };

  const queueLine = (tabIdx: number, line: string | LogLine) => {
    const pending = pendingLines.current.get(tabIdx) ?? [];
    pending.push(line);
    pendingLines.current.set(tabIdx, pending);
    if (!flushRafId.current) {
      flushRafId.current = requestAnimationFrame(flushPendingLines);
    }
  };

  // A tab's stream ended with a reason. A retryable end resumes on the same
  // cluster after a backoff, refreshing the session first when the token
  // expired; otherwise, or once the tries are spent, the end is shown.
  const endTab = (tabIdx: number, frame: StreamErrorFrame) => {
    closeTabWS(tabIdx);
    const tries = triesRef.current.get(tabIdx) ?? 0;
    const delay = RECONNECT_DELAYS_MS[tries];
    if (frame.retryable && delay !== undefined) {
      triesRef.current.set(tabIdx, tries + 1);
      updateTabState(tabIdx, { connected: false, reconnecting: true });
      const epoch = resumeEpoch.current.get(tabIdx);
      retryTimers.current.set(
        tabIdx,
        setTimeout(async () => {
          retryTimers.current.delete(tabIdx);
          if (frame.reason === "token_expired") await refreshSession();
          if (resumeEpoch.current.get(tabIdx) !== epoch) return;
          if (mode.value === "follow") connectWS(tabIdx, true);
        }, delay),
      );
      return;
    }
    updateTabState(tabIdx, {
      connected: false,
      reconnecting: false,
      error: streamErrorMessage(frame, "Live log streaming"),
    });
  };

  // "Reconnect" resumes with a fresh set of tries.
  const reconnectTab = (tabIdx: number) => {
    triesRef.current.set(tabIdx, 0);
    connectWS(tabIdx, true);
  };

  // Connect WS for follow mode. A resume keeps the lines already shown.
  const connectWS = useCallback(
    (tabIdx: number, resume = false) => {
      const container = allContainers[tabIdx]?.name;
      if (!container) return;

      // Close existing connection for this tab
      closeTabWS(tabIdx);
      if (resume) {
        resumeAfter.current.set(
          tabIdx,
          lastTimestamp.current.get(tabIdx) ?? null,
        );
      } else {
        triesRef.current.set(tabIdx, 0);
        resumeAfter.current.delete(tabIdx);
      }

      const proto = globalThis.location.protocol === "https:" ? "wss:" : "ws:";
      const url = `${proto}//${globalThis.location.host}/ws/v1/ws/logs/${namespace}/${pod}/${container}`;

      const ws = new WebSocket(url);
      wsRefs.current.set(tabIdx, ws);
      // A resume stays "Reconnecting..." until it subscribes or ends.
      updateTabState(tabIdx, {
        error: "",
        connected: false,
        ...(resume ? {} : { reconnecting: false }),
      });
      // A resume that closes before it subscribes, with no end frame (the
      // backend still down behind the proxy), is a failed try, not an end.
      let subscribed = false;
      let ended = false;

      ws.onopen = () => {
        const token = getAccessToken();
        if (!token) {
          ws.close();
          wsRefs.current.delete(tabIdx);
          updateTabState(tabIdx, {
            error: "Not authenticated",
            reconnecting: false,
          });
          return;
        }
        pinRef.current ??= currentTarget();
        ws.send(JSON.stringify(streamAuthFrame(token, pinRef.current)));
        ws.send(
          JSON.stringify({
            container,
            tailLines: 500,
            previous: previous.value,
            timestamps: true,
          }),
        );
      };

      ws.onmessage = (event) => {
        // Mode guard: discard messages if we've switched to snapshot mode,
        // or if this socket was already replaced
        if (mode.value !== "follow" || wsRefs.current.get(tabIdx) !== ws) {
          return;
        }

        try {
          const msg = JSON.parse(event.data);
          if (msg.type === "subscribed") {
            subscribed = true;
            triesRef.current.set(tabIdx, 0);
            updateTabState(tabIdx, {
              connected: true,
              reconnecting: false,
              error: "",
            });
            loading.value = false;
            if (resume) queueLine(tabIdx, RECONNECTED_MARKER);
            return;
          }
          if (msg.type === "log") {
            const line = String(msg.data ?? "");
            if (resumeAfter.current.has(tabIdx)) {
              if (
                isReplayedLine(line, resumeAfter.current.get(tabIdx) ?? null)
              ) {
                return;
              }
              // The first line past the join ends the replay.
              resumeAfter.current.delete(tabIdx);
            }
            const ts = logLineTimestamp(line);
            if (ts) lastTimestamp.current.set(tabIdx, ts);
            // Batch into pending buffer — flushed once per RAF
            queueLine(tabIdx, line);
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
            // An end with a reason follows the lifecycle; a frame without
            // one (a bad container name, a failed open) keeps its message.
            if (msg.reason) {
              endTab(tabIdx, msg);
            } else {
              ended = true;
              updateTabState(tabIdx, {
                error: streamErrorMessage(msg, "Live log streaming"),
                reconnecting: false,
              });
            }
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

      // A socket this tab already replaced (a resume closes the old one
      // first) must not touch the tab's state or its new socket.
      ws.onclose = () => {
        if (wsRefs.current.get(tabIdx) !== ws) return;
        wsRefs.current.delete(tabIdx);
        if (resume && !subscribed && !ended) {
          endTab(tabIdx, { reason: "connect_failed", retryable: true });
          return;
        }
        updateTabState(tabIdx, { connected: false });
      };

      // A failed resume is left to onclose, which always follows an error.
      ws.onerror = () => {
        if (wsRefs.current.get(tabIdx) !== ws) return;
        if (resume && !subscribed && !ended) return;
        updateTabState(tabIdx, { error: "WebSocket connection failed" });
      };
    },
    [namespace, pod, previous.value],
  );

  // Fetch logs via HTTP (snapshot mode)
  const fetchSnapshot = useCallback(
    async (tabIdx: number) => {
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
        // The server's own message ("this container has no previous
        // instance…") reads better than "API error 404: …".
        const message =
          err instanceof ApiError && err.body?.error?.message
            ? err.body.error.message
            : err instanceof Error
              ? err.message
              : "Failed to fetch logs";
        updateTabState(tabIdx, { lines: [], error: message });
        loading.value = false;
      }
    },
    [namespace, pod, previous.value],
  );

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
      // Close ALL WS connections, and cancel any resume, when switching to
      // snapshot mode
      for (const i of new Set([
        ...wsRefs.current.keys(),
        ...retryTimers.current.keys(),
      ])) {
        closeTabWS(i);
        updateTabState(i, { connected: false, reconnecting: false });
      }
      fetchSnapshot(idx);
    }

    return () => {
      // Cleanup all WS and pending resumes on unmount or dependency change
      for (const [, ws] of wsRefs.current) {
        ws.close();
      }
      wsRefs.current.clear();
      for (let i = 0; i < allContainers.length; i++) cancelResume(i);
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
    return filteredLines
      .map((l) => {
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
      })
      .join("\n");
  };

  // Download logs
  const downloadLogs = (tabIdx: number) => {
    const state = getTabState(tabIdx);
    const content = state.lines.map((l) => l.raw).join("\n");
    const blob = new Blob([content], { type: "text/plain" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = `${pod}-${allContainers[tabIdx]?.name ?? "unknown"}-${new Date()
      .toISOString()
      .replace(/[:.]/g, "-")}.log`;
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
        // ANSI colour rendering requires innerHTML; input is escaped by ansi_up.
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
                ? "bg-brand"
                : "text-text-secondary hover:bg-elevated text-text-secondary"
            } rounded-l-md`}
            style={
              mode.value === "follow" ? { color: "var(--bg-base)" } : undefined
            }
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
                ? "bg-brand"
                : "text-text-secondary hover:bg-elevated text-text-secondary"
            } rounded-r-md`}
            style={
              mode.value === "snapshot"
                ? { color: "var(--bg-base)" }
                : undefined
            }
          >
            Snapshot
          </button>
        </div>

        <label class="flex items-center gap-1.5 text-sm text-text-secondary">
          <input
            type="checkbox"
            checked={previous.value}
            onChange={(e) => {
              // The lines shown belong to the other instance; keeping them
              // would put a "no previous instance" error above current logs.
              lastTimestamp.current.clear();
              resumeAfter.current.clear();
              tabStates.value = tabStates.value.map((s) => ({
                ...s,
                lines: [],
                droppedCount: 0,
                error: "",
              }));
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
                  ? "bg-success animate-pulse"
                  : "bg-text-muted"
              }`}
            />
            {currentState.connected
              ? "Streaming"
              : currentState.reconnecting
                ? "Reconnecting..."
                : "Disconnected"}
          </span>
        )}
        {mode.value === "follow" && pinRef.current && (
          <SessionClusterBadge clusterId={pinRef.current.clusterId} />
        )}
        {mode.value === "follow" &&
          currentState.error &&
          !currentState.connected &&
          !currentState.reconnecting && (
            <Button
              variant="ghost"
              size="sm"
              onClick={() => reconnectTab(activeTab.value)}
            >
              Reconnect
            </Button>
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
      {currentState.error && <ErrorBanner message={currentState.error} />}

      {/* Log output */}
      {splitView.value ? (
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
      ) : (
        renderLogPanel(activeTab.value)
      )}

      {/* Resume auto-scroll FAB */}
      {!autoScroll.value && mode.value === "follow" && (
        <button
          type="button"
          onClick={() => {
            autoScroll.value = true;
            const pre = preRefs.current[activeTab.value];
            if (pre) pre.scrollTop = pre.scrollHeight;
          }}
          // right-20 clears the 48px quick-actions button that sits at the
          // same corner (QuickActionsFab: bottom/right 24px, above z-30).
          class="fixed bottom-6 right-20 z-30 rounded-full bg-brand px-4 py-2 text-sm font-medium shadow-lg hover:bg-brand/90"
          style={{ color: "var(--bg-base)" }}
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
