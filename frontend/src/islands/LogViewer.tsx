import { useEffect, useMemo, useRef, useState } from "react";
import { Button } from "@/components/ui/Button.tsx";
import { ErrorBanner } from "@/components/ui/ErrorBanner.tsx";
import { SearchBar } from "@/components/ui/SearchBar.tsx";
import {
  ApiError,
  apiGet,
  getAccessToken,
  readyAccessToken,
  refreshSession,
} from "@/lib/api.ts";
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
import { useIsClient } from "@/src/lib/use-is-client.ts";
import { useLatest } from "@/src/lib/use-latest.ts";
import { useStateRef } from "@/src/lib/use-state-ref.ts";

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

// ansi_up ships no TypeScript types in its npm package.
interface AnsiUpInstance {
  use_classes: boolean;
  escape_html: boolean;
  ansi_to_html(text: string): string;
}
type AnsiUpConstructor = new () => AnsiUpInstance;
interface AnsiUpModule {
  AnsiUp?: AnsiUpConstructor;
  default?: AnsiUpConstructor;
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
  const isClient = useIsClient();
  const [activeTab, setActiveTab] = useState(0);
  const [mode, setMode, modeRef] = useStateRef<"follow" | "snapshot">("follow");
  const [search, setSearch] = useState("");
  const [previous, setPrevious] = useState(false);
  const [splitView, setSplitView] = useState(false);
  const [splitTab, setSplitTab] = useState(1); // second tab in split view
  // Per-tab state (display-relevant data only)
  const [tabStates, setTabStates] = useState<TabState[]>(() =>
    containers.map(() => EMPTY_TAB),
  );
  const [loading, setLoading] = useState(true);
  const [autoScroll, setAutoScroll, autoScrollRef] = useStateRef(true);
  const preRefs = useRef<(HTMLPreElement | null)[]>([]);
  const ansiUpRef = useRef<AnsiUpInstance | null>(null);

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

  // Socket callbacks, timers and frames run long after the render that
  // created them, so they read these instead of rendered values. Mode and
  // auto-scroll are written the moment a handler changes them: the stream
  // guard and the next flush must see the change before the re-render.
  const live = useLatest({ namespace, pod, allContainers });
  const previousRef = useRef(previous);
  const activeTabRef = useLatest(activeTab);
  const splitViewRef = useLatest(splitView);
  const splitTabRef = useLatest(splitTab);

  // WS refs — sockets are not render data
  const wsRefs = useRef<Map<number, WebSocket>>(new Map());
  // The cluster this viewer streams from, taken once when it opens and kept
  // across every reconnect, so a resume never follows a changed selection.
  // Taken on the first render after hydration, which is the first one that
  // can show the badge.
  const pinRef = useRef<ClusterTarget | null>(null);
  if (isClient && pinRef.current === null) pinRef.current = currentTarget();

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
  // Bumped by each snapshot fetch and by the Previous toggle; a snapshot
  // answer from an older generation is dropped.
  const snapshotGen = useRef(0);
  const scrollRafId = useRef(0);

  // Load ansi_up dynamically (browser-only)
  useEffect(() => {
    import("ansi_up").then((mod) => {
      // This package's ESM build has no default export at all -- it exports
      // only the named `AnsiUp` class, so a `mod.default ?? mod` fallback
      // picks the whole module namespace object (not a constructor) and
      // throws on `new AnsiUp()`. Prefer the named export, keep both
      // fallbacks for whichever shape a future bundler/version produces.
      const shapes = mod as unknown as AnsiUpModule;
      const AnsiUp = shapes.AnsiUp ?? shapes.default ?? shapes;
      const instance = new (AnsiUp as AnsiUpConstructor)();
      instance.use_classes = true;
      instance.escape_html = true;
      ansiUpRef.current = instance;
    });
  }, []);

  // Get current tab state
  const getTabState = (idx: number): TabState => tabStates[idx] ?? EMPTY_TAB;

  const updateTabState = (idx: number, update: Partial<TabState>) => {
    setTabStates((prev) => {
      const states = [...prev];
      states[idx] = { ...(states[idx] ?? EMPTY_TAB), ...update };
      return states;
    });
  };

  // Convert a raw log line to a LogLine with cached HTML
  const toLogLine = (raw: string): LogLine => {
    const ansi = ansiUpRef.current;
    return {
      raw,
      html: ansi ? ansi.ansi_to_html(raw) : escapeHtml(raw),
    };
  };

  // Flush pending log lines from the buffer into state (called once per RAF)
  const flushPendingLines = () => {
    flushRafId.current = 0;

    const batches: [number, LogLine[]][] = [];
    for (const [idx, rawLines] of pendingLines.current) {
      if (rawLines.length === 0) continue;
      batches.push([
        idx,
        rawLines.map((l) => (typeof l === "string" ? toLogLine(l) : l)),
      ]);
    }
    pendingLines.current.clear();

    const drops: [number, number][] = [];
    for (const [idx, count] of pendingDrops.current) {
      if (count > 0) drops.push([idx, count]);
    }
    pendingDrops.current.clear();

    if (batches.length === 0 && drops.length === 0) return;

    setTabStates((prev) => {
      const states = [...prev];
      for (const [idx, logLines] of batches) {
        const current = states[idx] ?? EMPTY_TAB;
        const merged = [...current.lines, ...logLines];
        states[idx] = {
          ...current,
          lines:
            merged.length > MAX_LINES
              ? merged.slice(merged.length - MAX_LINES)
              : merged,
        };
      }
      for (const [idx, count] of drops) {
        const current = states[idx] ?? EMPTY_TAB;
        states[idx] = {
          ...current,
          droppedCount: current.droppedCount + count,
        };
      }
      return states;
    });

    // Auto-scroll after batch flush
    if (autoScrollRef.current && !scrollRafId.current) {
      scrollRafId.current = requestAnimationFrame(() => {
        scrollRafId.current = 0;
        const pre = preRefs.current[activeTabRef.current];
        if (pre) pre.scrollTop = pre.scrollHeight;
      });
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
          if (modeRef.current === "follow") connectWS(tabIdx, true);
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
  const connectWS = (tabIdx: number, resume = false) => {
    const container = live.current.allContainers[tabIdx]?.name;
    if (!container) return;
    const { namespace: ns, pod: podName } = live.current;

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
    const url = `${proto}//${globalThis.location.host}/ws/v1/ws/logs/${ns}/${podName}/${container}`;

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

    // Authenticates now when a token is in hand, else once the page's
    // startup refresh lands, unless the socket was replaced meanwhile.
    ws.onopen = () => {
      const token = getAccessToken();
      if (token) {
        authenticate(token);
        return;
      }
      void readyAccessToken().then((ready) => {
        if (wsRefs.current.get(tabIdx) === ws) authenticate(ready);
      });
    };

    const authenticate = (token: string | null) => {
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
          previous: previousRef.current,
          timestamps: true,
        }),
      );
    };

    ws.onmessage = (event) => {
      // Mode guard: discard messages if we've switched to snapshot mode,
      // or if this socket was already replaced
      if (modeRef.current !== "follow" || wsRefs.current.get(tabIdx) !== ws) {
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
          setLoading(false);
          if (resume) queueLine(tabIdx, RECONNECTED_MARKER);
          return;
        }
        if (msg.type === "log") {
          const line = String(msg.data ?? "");
          if (resumeAfter.current.has(tabIdx)) {
            if (isReplayedLine(line, resumeAfter.current.get(tabIdx) ?? null)) {
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
  };

  // Fetch logs via HTTP (snapshot mode)
  const fetchSnapshot = async (tabIdx: number) => {
    const container = live.current.allContainers[tabIdx]?.name;
    if (!container) return;
    const { namespace: ns, pod: podName } = live.current;
    const gen = ++snapshotGen.current;
    setLoading(true);
    updateTabState(tabIdx, { error: "" });
    try {
      const params = new URLSearchParams({
        container,
        tailLines: "500",
        previous: String(previousRef.current),
        timestamps: "true",
      });
      const res = await apiGet<LogResponse>(
        `/v1/resources/pods/${ns}/${podName}/logs?${params}`,
      );
      if (gen !== snapshotGen.current) return;
      updateTabState(tabIdx, {
        lines: (res.data.lines ?? []).map(toLogLine),
      });
      setLoading(false);
      // Scroll to bottom
      requestAnimationFrame(() => {
        const pre = preRefs.current[tabIdx];
        if (pre) pre.scrollTop = pre.scrollHeight;
      });
    } catch (err) {
      if (gen !== snapshotGen.current) return;
      const message =
        err instanceof ApiError && err.body?.error?.message
          ? err.body.error.message
          : err instanceof Error
            ? err.message
            : "Failed to fetch logs";
      updateTabState(tabIdx, { lines: [], error: message });
      setLoading(false);
    }
  };

  // The connect effect re-runs on a tab, mode or Previous change only, so it
  // calls this render's handlers through a ref rather than depending on them.
  const actions = useLatest({
    connectWS,
    fetchSnapshot,
    closeTabWS,
    cancelResume,
  });

  // Connect/fetch on mode or tab change
  useEffect(() => {
    const run = actions.current;
    previousRef.current = previous;

    if (mode === "follow") {
      run.connectWS(activeTab);
      if (splitViewRef.current && splitTabRef.current !== activeTab) {
        run.connectWS(splitTabRef.current);
      }
    } else {
      // Close ALL WS connections, and cancel any resume, when switching to
      // snapshot mode
      for (const i of new Set([
        ...wsRefs.current.keys(),
        ...retryTimers.current.keys(),
      ])) {
        run.closeTabWS(i);
        setTabStates((prev) => {
          const states = [...prev];
          states[i] = {
            ...(states[i] ?? EMPTY_TAB),
            connected: false,
            reconnecting: false,
          };
          return states;
        });
      }
      run.fetchSnapshot(activeTab);
    }

    return () => {
      // Cleanup all WS and pending resumes on unmount or dependency change
      for (const [, ws] of wsRefs.current) {
        ws.close();
      }
      wsRefs.current.clear();
      for (let i = 0; i < live.current.allContainers.length; i++) {
        actions.current.cancelResume(i);
      }
      // Cancel pending RAFs
      if (flushRafId.current) cancelAnimationFrame(flushRafId.current);
      if (scrollRafId.current) cancelAnimationFrame(scrollRafId.current);
      flushRafId.current = 0;
      scrollRafId.current = 0;
      pendingLines.current.clear();
      pendingDrops.current.clear();
    };
  }, [activeTab, mode, previous]);

  // Scroll detection for auto-scroll pause
  const handleScroll = (tabIdx: number) => {
    const pre = preRefs.current[tabIdx];
    if (!pre) return;
    const atBottom = pre.scrollTop + pre.clientHeight >= pre.scrollHeight - 20;
    if (!atBottom && autoScrollRef.current) {
      setAutoScroll(false);
    }
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

  // Search filtering and the panel HTML, per tab, on the inputs they read.
  const activeLines = (tabStates[activeTab] ?? EMPTY_TAB).lines;
  const splitLines = splitView
    ? (tabStates[splitTab] ?? EMPTY_TAB).lines
    : EMPTY_TAB.lines;
  const activeFiltered = useMemo(
    () => filterLines(activeLines, search),
    [activeLines, search],
  );
  const splitFiltered = useMemo(
    () => filterLines(splitLines, search),
    [splitLines, search],
  );
  const activeHtml = useMemo(
    () => logPanelHtml(activeFiltered, search, loading),
    [activeFiltered, search, loading],
  );
  const splitHtml = useMemo(
    () => logPanelHtml(splitFiltered, search, loading),
    [splitFiltered, search, loading],
  );

  if (!isClient) return null;

  const currentState = getTabState(activeTab);
  const matchCount = search.trim() ? activeFiltered.length : 0;

  const renderLogPanel = (tabIdx: number, html: string) => {
    return (
      <pre
        ref={(el) => {
          preRefs.current[tabIdx] = el;
        }}
        // ANSI colour rendering needs markup; ansi_up runs with escape_html
        // set, so the log text itself is escaped before any span is added.
        // biome-ignore lint/security/noDangerouslySetInnerHtml: escaped by ansi_up, see above
        dangerouslySetInnerHTML={{ __html: html }}
        className="h-[600px] overflow-auto rounded-lg bg-base p-4 font-mono text-xs leading-5 text-text-primary"
        onScroll={() => handleScroll(tabIdx)}
      />
    );
  };

  return (
    <div className="flex flex-col gap-3">
      {/* Container tabs */}
      {allContainers.length > 1 && (
        <div className="flex items-center gap-1 border-b border-border-primary">
          {allContainers.map((c, idx) => (
            <button
              key={c.name}
              type="button"
              onClick={() => {
                setActiveTab(idx);
              }}
              className={`px-3 py-1.5 text-sm font-medium border-b-2 transition-colors ${
                activeTab === idx
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
      <div className="flex flex-wrap items-center gap-3">
        {/* Mode toggle */}
        <div className="flex rounded-md border border-border-primary">
          <button
            type="button"
            onClick={() => {
              setMode("follow");
            }}
            className={`px-3 py-1 text-xs font-medium ${
              mode === "follow"
                ? "bg-brand"
                : "text-text-secondary hover:bg-elevated text-text-secondary"
            } rounded-l-md`}
            style={mode === "follow" ? { color: "var(--bg-base)" } : undefined}
          >
            Follow
          </button>
          <button
            type="button"
            onClick={() => {
              setMode("snapshot");
            }}
            className={`px-3 py-1 text-xs font-medium ${
              mode === "snapshot"
                ? "bg-brand"
                : "text-text-secondary hover:bg-elevated text-text-secondary"
            } rounded-r-md`}
            style={
              mode === "snapshot" ? { color: "var(--bg-base)" } : undefined
            }
          >
            Snapshot
          </button>
        </div>

        <label className="flex items-center gap-1.5 text-sm text-text-secondary">
          <input
            type="checkbox"
            checked={previous}
            onChange={(e) => {
              // Stop the other instance's stream now, not at the effect's
              // cleanup a frame later: its socket, its buffered lines and a
              // snapshot still in flight would land in the cleared tabs.
              for (const i of [...wsRefs.current.keys()]) closeTabWS(i);
              pendingLines.current.clear();
              pendingDrops.current.clear();
              if (flushRafId.current) cancelAnimationFrame(flushRafId.current);
              flushRafId.current = 0;
              snapshotGen.current++;
              lastTimestamp.current.clear();
              resumeAfter.current.clear();
              setTabStates((prev) =>
                prev.map((s) => ({
                  ...s,
                  lines: [],
                  droppedCount: 0,
                  error: "",
                })),
              );
              setPrevious(e.currentTarget.checked);
            }}
          />
          Previous
        </label>

        {/* Split view (only for multi-container pods) */}
        {allContainers.length >= 2 && (
          <label className="flex items-center gap-1.5 text-sm text-text-secondary">
            <input
              type="checkbox"
              checked={splitView}
              onChange={(e) => {
                const checked = e.currentTarget.checked;
                if (checked) {
                  // Pick second container different from active
                  const other = activeTab === 0 ? 1 : 0;
                  setSplitTab(other);
                  if (modeRef.current === "follow") connectWS(other);
                  else fetchSnapshot(other);
                } else {
                  // Close the split tab's WS when disabling split view
                  closeTabWS(splitTab);
                  updateTabState(splitTab, { connected: false });
                }
                setSplitView(checked);
              }}
            />
            Split view
          </label>
        )}

        {mode === "snapshot" && (
          <Button
            variant="ghost"
            size="sm"
            onClick={() => fetchSnapshot(activeTab)}
          >
            Refresh
          </Button>
        )}

        <Button
          variant="ghost"
          size="sm"
          onClick={() => downloadLogs(activeTab)}
        >
          Download
        </Button>

        {/* Status indicators */}
        {mode === "follow" && (
          <span
            className={`inline-flex items-center gap-1 text-xs ${
              currentState.connected ? "text-success" : "text-text-muted"
            }`}
          >
            <span
              className={`inline-block h-2 w-2 rounded-full ${
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
        {mode === "follow" && pinRef.current && (
          <SessionClusterBadge clusterId={pinRef.current.clusterId} />
        )}
        {mode === "follow" &&
          currentState.error &&
          !currentState.connected &&
          !currentState.reconnecting && (
            <Button
              variant="ghost"
              size="sm"
              onClick={() => reconnectTab(activeTab)}
            >
              Reconnect
            </Button>
          )}

        <span className="text-xs text-text-muted">
          {getTabState(activeTab).lines.length} lines
          {currentState.droppedCount > 0 &&
            ` (${currentState.droppedCount} dropped)`}
        </span>
      </div>

      {/* Search */}
      <div className="flex items-center gap-2">
        <div className="max-w-sm flex-1">
          <SearchBar
            value={search}
            onInput={(v) => {
              setSearch(v);
            }}
            placeholder="Search logs..."
          />
        </div>
        {search && (
          <span className="text-xs text-text-muted">
            {matchCount} matching lines
          </span>
        )}
      </div>

      {/* Error */}
      {currentState.error && <ErrorBanner message={currentState.error} />}

      {/* Log output */}
      {splitView ? (
        <div className="grid grid-cols-2 gap-2">
          <div>
            <div className="mb-1 text-xs font-medium text-text-muted">
              {allContainers[activeTab]?.name}
            </div>
            {renderLogPanel(activeTab, activeHtml)}
          </div>
          <div>
            <div className="mb-1 text-xs font-medium text-text-muted">
              {allContainers[splitTab]?.name}
            </div>
            {renderLogPanel(splitTab, splitHtml)}
          </div>
        </div>
      ) : (
        renderLogPanel(activeTab, activeHtml)
      )}

      {/* Resume auto-scroll FAB */}
      {!autoScroll && mode === "follow" && (
        <button
          type="button"
          onClick={() => {
            setAutoScroll(true);
            const pre = preRefs.current[activeTab];
            if (pre) pre.scrollTop = pre.scrollHeight;
          }}
          // right-20 clears the 48px quick-actions button that sits at the
          // same corner (QuickActionsFab: bottom/right 24px, above z-30).
          className="fixed bottom-6 right-20 z-30 rounded-full bg-brand px-4 py-2 text-sm font-medium shadow-lg hover:bg-brand/90"
          style={{ color: "var(--bg-base)" }}
        >
          Resume auto-scroll
        </button>
      )}
    </div>
  );
}

/** Search filtering: operates on raw text. */
function filterLines(lines: LogLine[], search: string): LogLine[] {
  if (!search.trim()) return lines;
  const q = search.toLowerCase();
  return lines.filter((line) => line.raw.toLowerCase().includes(q));
}

/** Log content using pre-cached HTML + search highlighting on raw text. */
function logPanelHtml(
  filteredLines: LogLine[],
  search: string,
  loading: boolean,
): string {
  if (filteredLines.length === 0) {
    if (loading) return "Loading logs...";
    return "No log output";
  }

  const q = search.trim().toLowerCase();

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
}

function escapeHtml(s: string): string {
  return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}

function escapeRegExp(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}
