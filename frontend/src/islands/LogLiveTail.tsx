/** @jsxImportSource react */
import {
  type Dispatch,
  type SetStateAction,
  useEffect,
  useRef,
  useState,
} from "react";
import { Button } from "@/components/ui/Button.tsx";
import { getAccessToken, readyAccessToken, refreshSession } from "@/lib/api.ts";
import {
  lokiLaterThan,
  RECONNECT_DELAYS_MS,
  type StreamErrorFrame,
  streamAuthFrame,
  streamErrorMessage,
} from "@/lib/stream-state.ts";
import type { LogLine } from "@/lib/types/logs.ts";
import { SessionClusterBadge } from "@/src/components/SessionClusterBadge.tsx";
import { type ClusterTarget, currentTarget } from "@/src/lib/cluster.ts";
import { IS_BROWSER } from "@/src/lib/is-browser.ts";
import { useIsClient } from "@/src/lib/use-is-client.ts";
import { useLatest } from "@/src/lib/use-latest.ts";
import { useStateRef } from "@/src/lib/use-state-ref.ts";

interface LogLiveTailProps {
  active: boolean;
  query: string;
  namespace: string;
  lines: LogLine[];
  setLines: Dispatch<SetStateAction<LogLine[]>>;
}

type TailStatus = "connecting" | "connected" | "reconnecting" | "disconnected";

const MAX_LINES = 5000;

/** A Loki timestamp the tail can send back as its `start`: an int64. */
const NANOS = /^\d{1,19}$/;

/**
 * Where a resumed tail rejoins the lines already shown. It has no
 * timestamp, which is how the list tells it from a log line.
 */
const RECONNECTED_MARKER: LogLine = {
  timestamp: "",
  line: "--- reconnected ---",
  labels: {},
};

/** The subscribe frame. A resume asks Loki to start at the last line shown. */
function subscribeFrame(
  query: string,
  namespace: string,
  start: string | null,
) {
  const frame = JSON.stringify({
    type: "subscribe",
    query,
    namespace,
    limit: 30,
  });
  // The server reads `start` as an int64 of nanoseconds, more digits than a
  // JS number holds exactly, so the digits are written into the JSON as
  // they came from Loki rather than through a number.
  if (start === null || !NANOS.test(start)) return frame;
  return `${frame.slice(0, -1)},"start":${start}}`;
}

export default function LogLiveTail(props: LogLiveTailProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const wsRef = useRef<WebSocket | null>(null);
  // The cluster this tail streams from, taken once when it opens and kept
  // across every reconnect and query change, so a resume never follows a
  // changed selection (KTD1).
  const [pin] = useState<ClusterTarget>(currentTarget);

  // Resume state. A retryable end reconnects after a backoff from the last
  // line shown; Loki's start is inclusive, so lines at or before it are
  // skipped and one marker shows the join.
  const triesRef = useRef(0);
  const retryTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  // Bumped whenever a pending resume is cancelled. A resume that awaited a
  // session refresh checks it before connecting, so one that outlived its
  // query or the tail opens nothing.
  const resumeEpoch = useRef(0);
  const lastTimestamp = useRef<string | null>(null);
  const resumeAfter = useRef<string | null>(null);

  const isClient = useIsClient();
  const [autoScroll, setAutoScroll, autoScrollRef] = useStateRef(true);
  const [status, setStatus] = useState<TailStatus>("disconnected");
  const [error, setError] = useState("");

  // The socket and timer callbacks below outlive the render that made them,
  // so they read the query, namespace and line setter through these.
  const queryRef = useLatest(props.query);
  const namespaceRef = useLatest(props.namespace);
  const setLinesRef = useLatest(props.setLines);

  // Auto-scroll detection
  function handleScroll() {
    const el = containerRef.current;
    if (!el) return;
    const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 50;
    setAutoScroll(atBottom);
  }

  function resumeScroll() {
    setAutoScroll(true);
    const el = containerRef.current;
    if (el) el.scrollTop = el.scrollHeight;
  }

  function appendLines(newLines: LogLine[]) {
    if (newLines.length === 0) return;
    setLinesRef.current((prev) => {
      const updated = [...prev, ...newLines];
      return updated.length > MAX_LINES
        ? updated.slice(updated.length - MAX_LINES)
        : updated;
    });
    if (autoScrollRef.current) {
      requestAnimationFrame(() => {
        const el = containerRef.current;
        if (el) el.scrollTop = el.scrollHeight;
      });
    }
  }

  // Cancel a pending resume, including one already past its timer and
  // waiting on a session refresh.
  function cancelResume() {
    resumeEpoch.current++;
    if (retryTimer.current) {
      clearTimeout(retryTimer.current);
      retryTimer.current = null;
    }
  }

  function closeWS() {
    cancelResume();
    const ws = wsRef.current;
    if (ws) {
      wsRef.current = null;
      ws.close();
    }
  }

  // The stream ended with a reason. A retryable end resumes on the same
  // cluster after a backoff, refreshing the session first when the token
  // expired; otherwise, or once the tries are spent, the end is shown.
  function endTail(frame: StreamErrorFrame) {
    closeWS();
    const delay = RECONNECT_DELAYS_MS[triesRef.current];
    if (frame.retryable && delay !== undefined) {
      triesRef.current++;
      setStatus("reconnecting");
      const epoch = resumeEpoch.current;
      retryTimer.current = setTimeout(async () => {
        retryTimer.current = null;
        if (frame.reason === "token_expired") await refreshSession();
        if (resumeEpoch.current !== epoch) return;
        connect(true);
      }, delay);
      return;
    }
    setStatus("disconnected");
    setError(streamErrorMessage(frame, "Live log search"));
  }

  // "Reconnect" resumes with a fresh set of tries.
  function reconnect() {
    triesRef.current = 0;
    connect(true);
  }

  // Open the tail. A resume keeps the lines already shown and starts from
  // the last one.
  function connect(resume: boolean) {
    closeWS();
    if (resume) {
      resumeAfter.current = lastTimestamp.current;
    } else {
      triesRef.current = 0;
      resumeAfter.current = null;
      lastTimestamp.current = null;
    }

    // A resume stays "reconnecting" until it subscribes or ends.
    if (!resume) setStatus("connecting");
    setError("");
    const protocol = location.protocol === "https:" ? "wss:" : "ws:";
    const ws = new WebSocket(
      `${protocol}//${location.host}/ws/v1/ws/logs-search`,
    );
    wsRef.current = ws;
    // A resume that closes before it subscribes, with no end frame (the
    // backend still down behind the proxy), is a failed try, not an end.
    let subscribed = false;
    let ended = false;
    // Whether this socket delivered a log line. Only a stream that carried
    // lines resets the retry budget: a Loki that accepts the tail and drops
    // it at once must still run out of tries.
    let streamed = false;

    // Authenticates now when a token is in hand, else once the page's
    // startup refresh lands, unless the socket was replaced meanwhile.
    ws.onopen = () => {
      if (wsRef.current !== ws) return;
      const token = getAccessToken();
      if (token) {
        authenticate(token);
        return;
      }
      void readyAccessToken().then((ready) => {
        if (wsRef.current === ws) authenticate(ready);
      });
    };

    const authenticate = (token: string | null) => {
      if (!token) {
        ended = true;
        wsRef.current = null;
        ws.close();
        setStatus("disconnected");
        setError("Not authenticated");
        return;
      }
      ws.send(JSON.stringify(streamAuthFrame(token, pin)));
      ws.send(
        subscribeFrame(
          queryRef.current,
          namespaceRef.current,
          resume ? resumeAfter.current : null,
        ),
      );
    };

    ws.onmessage = (event) => {
      // A socket this tail already replaced must not touch its state.
      if (wsRef.current !== ws) return;
      try {
        const msg = JSON.parse(event.data);
        if (msg.type === "subscribed") {
          subscribed = true;
          setStatus("connected");
          setError("");
          if (resume) appendLines([RECONNECTED_MARKER]);
          return;
        }
        if (msg.type === "error") {
          // An end with a reason follows the lifecycle; a frame without one
          // (a missing query, a refused namespace) keeps its message.
          if (msg.reason) {
            endTail(msg);
          } else {
            ended = true;
            setStatus("disconnected");
            setError(streamErrorMessage(msg, "Live log search"));
          }
          return;
        }
        if (msg.type === "log" && msg.payload) {
          if (!streamed) {
            streamed = true;
            triesRef.current = 0;
          }
          const payload =
            typeof msg.payload === "string"
              ? JSON.parse(msg.payload)
              : msg.payload;
          const after = resumeAfter.current;
          const newLines: LogLine[] = [];
          for (const stream of payload.streams ?? []) {
            for (const [ts, line] of stream.values ?? []) {
              const timestamp = String(ts);
              if (after !== null && !lokiLaterThan(timestamp, after)) continue;
              if (
                lastTimestamp.current === null ||
                lokiLaterThan(timestamp, lastTimestamp.current)
              ) {
                lastTimestamp.current = timestamp;
              }
              newLines.push({ timestamp, line, labels: stream.stream ?? {} });
            }
          }
          // The first batch past the join ends the replay.
          if (newLines.length > 0) resumeAfter.current = null;
          appendLines(newLines);
        }
      } catch {
        /* ignore malformed messages */
      }
    };

    ws.onclose = () => {
      if (wsRef.current !== ws) return;
      wsRef.current = null;
      if (resume && !subscribed && !ended) {
        endTail({ reason: "connect_failed", retryable: true });
        return;
      }
      setStatus("disconnected");
    };

    // A failed resume is left to onclose, which always follows an error.
    ws.onerror = () => {
      if (wsRef.current !== ws) return;
      if (resume && !subscribed && !ended) return;
      setStatus("disconnected");
    };
  }

  // The effect below runs on a tail start, stop or query change, not on every
  // render, so it reaches connect and closeWS through this.
  const tail = useLatest({ connect, closeWS });
  const { active, query } = props;

  // Connect/disconnect as the tail starts, stops or changes query.
  useEffect(() => {
    if (!IS_BROWSER || !active || !query) {
      tail.current.closeWS();
      setStatus("disconnected");
      return;
    }
    tail.current.connect(false);
    return () => tail.current.closeWS();
  }, [active, query]);

  if (!isClient || !props.active) return null;

  return (
    <div className="flex flex-col rounded-lg border border-border-primary">
      {/* Status bar */}
      <div className="flex items-center justify-between border-b border-border-primary bg-surface px-4 py-2">
        <div className="flex items-center gap-2 text-xs">
          <span
            className={`h-2 w-2 rounded-full ${
              status === "connected"
                ? "bg-success"
                : status === "disconnected"
                  ? "bg-danger"
                  : "bg-warning animate-pulse"
            }`}
          />
          <span className="text-text-muted capitalize">
            {status === "reconnecting" ? "Reconnecting..." : status}
          </span>
          <SessionClusterBadge clusterId={pin.clusterId} />
          <span className="text-text-muted">
            &middot; {props.lines.length} lines
          </span>
          {error && <span className="text-danger">&middot; {error}</span>}
          {error && status === "disconnected" && (
            <Button variant="ghost" size="sm" onClick={reconnect}>
              Reconnect
            </Button>
          )}
        </div>
        {!autoScroll && (
          <button
            type="button"
            className="rounded bg-accent px-2 py-0.5 text-xs text-(--bg-base) font-semibold"
            onClick={resumeScroll}
          >
            Resume scroll
          </button>
        )}
      </div>

      {/* Log output */}
      <div
        ref={containerRef}
        className="h-96 overflow-y-auto font-mono text-xs leading-relaxed"
        onScroll={handleScroll}
      >
        {props.lines.map((entry, i) => {
          if (!entry.timestamp) {
            return (
              // biome-ignore lint/suspicious/noArrayIndexKey: log lines have no id, and the list only ever appends or drops from the front
              <div key={i} className="px-4 py-0.5 text-text-muted">
                {entry.line}
              </div>
            );
          }
          const isError = /error/i.test(entry.line.slice(0, 100));
          return (
            <div
              // biome-ignore lint/suspicious/noArrayIndexKey: log lines have no id, and the list only ever appends or drops from the front
              key={i}
              className={`flex px-4 py-0.5 ${isError ? "bg-danger/5" : ""}`}
            >
              <span className="min-w-[140px] shrink-0 text-text-muted">
                {(() => {
                  try {
                    const ms = parseInt(entry.timestamp, 10) / 1_000_000;
                    return new Date(ms).toISOString().slice(11, 23);
                  } catch {
                    return "";
                  }
                })()}
              </span>
              {entry.labels?.pod && (
                <span className="min-w-[120px] shrink-0 text-info">
                  {entry.labels.pod.slice(0, 18)}
                </span>
              )}
              <span className="text-text-secondary break-all">
                {entry.line}
              </span>
            </div>
          );
        })}
      </div>
    </div>
  );
}
