import { type Signal, useSignal } from "@preact/signals";
import { useEffect, useRef } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";

interface LogLine {
  timestamp: string;
  line: string;
  labels: Record<string, string>;
}

interface LogLiveTailProps {
  active: Signal<boolean>;
  query: Signal<string>;
  namespace: Signal<string>;
  lines: Signal<LogLine[]>;
}

const MAX_LINES = 5000;

export default function LogLiveTail(props: LogLiveTailProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const wsRef = useRef<WebSocket | null>(null);
  const autoScroll = useSignal(true);
  const status = useSignal<"connecting" | "connected" | "disconnected">(
    "disconnected",
  );

  // Auto-scroll detection
  function handleScroll() {
    const el = containerRef.current;
    if (!el) return;
    const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 50;
    autoScroll.value = atBottom;
  }

  function resumeScroll() {
    autoScroll.value = true;
    const el = containerRef.current;
    if (el) el.scrollTop = el.scrollHeight;
  }

  // Connect/disconnect WebSocket
  useEffect(() => {
    if (!IS_BROWSER || !props.active.value || !props.query.value) {
      if (wsRef.current) {
        wsRef.current.close();
        wsRef.current = null;
        status.value = "disconnected";
      }
      return;
    }

    status.value = "connecting";
    const protocol = location.protocol === "https:" ? "wss:" : "ws:";
    const ws = new WebSocket(
      `${protocol}//${location.host}/ws/v1/ws/logs-search`,
    );
    wsRef.current = ws;

    ws.onopen = () => {
      // Send auth token
      const token = localStorage.getItem("access_token") ?? "";
      ws.send(JSON.stringify({ type: "auth", token }));

      // Send subscribe message
      ws.send(JSON.stringify({
        type: "subscribe",
        query: props.query.value,
        namespace: props.namespace.value,
        limit: 30,
      }));
    };

    ws.onmessage = (event) => {
      try {
        const msg = JSON.parse(event.data);
        if (msg.type === "subscribed") {
          status.value = "connected";
          return;
        }
        if (msg.type === "error") {
          status.value = "disconnected";
          return;
        }
        if (msg.type === "log" && msg.payload) {
          const payload = typeof msg.payload === "string"
            ? JSON.parse(msg.payload)
            : msg.payload;
          const newLines: LogLine[] = [];
          for (const stream of payload.streams ?? []) {
            for (const [ts, line] of stream.values ?? []) {
              newLines.push({
                timestamp: ts,
                line,
                labels: stream.stream ?? {},
              });
            }
          }
          if (newLines.length > 0) {
            const updated = [...props.lines.value, ...newLines];
            props.lines.value = updated.length > MAX_LINES
              ? updated.slice(updated.length - MAX_LINES)
              : updated;

            if (autoScroll.value) {
              requestAnimationFrame(() => {
                const el = containerRef.current;
                if (el) el.scrollTop = el.scrollHeight;
              });
            }
          }
        }
      } catch { /* ignore malformed messages */ }
    };

    ws.onclose = () => {
      status.value = "disconnected";
    };

    ws.onerror = () => {
      status.value = "disconnected";
    };

    return () => {
      ws.close();
      wsRef.current = null;
    };
  }, [props.active.value, props.query.value]);

  if (!IS_BROWSER || !props.active.value) return null;

  return (
    <div class="flex flex-col rounded-lg border border-border-primary">
      {/* Status bar */}
      <div class="flex items-center justify-between border-b border-border-primary bg-bg-surface px-4 py-2">
        <div class="flex items-center gap-2 text-xs">
          <span
            class={`h-2 w-2 rounded-full ${
              status.value === "connected"
                ? "bg-status-success"
                : status.value === "connecting"
                ? "bg-status-warning animate-pulse"
                : "bg-status-error"
            }`}
          />
          <span class="text-text-muted capitalize">{status.value}</span>
          <span class="text-text-muted">
            &middot; {props.lines.value.length} lines
          </span>
        </div>
        {!autoScroll.value && (
          <button
            type="button"
            class="rounded bg-accent-primary px-2 py-0.5 text-xs text-bg-base font-semibold"
            onClick={resumeScroll}
          >
            Resume scroll
          </button>
        )}
      </div>

      {/* Log output */}
      <div
        ref={containerRef}
        class="h-96 overflow-y-auto font-mono text-xs leading-relaxed"
        onScroll={handleScroll}
      >
        {props.lines.value.map((entry, i) => {
          const isError = /error/i.test(entry.line.slice(0, 100));
          return (
            <div
              key={i}
              class={`flex px-4 py-0.5 ${isError ? "bg-status-error/5" : ""}`}
            >
              <span class="min-w-[140px] shrink-0 text-text-muted">
                {(() => {
                  try {
                    const ms = parseInt(entry.timestamp) / 1_000_000;
                    return new Date(ms).toISOString().slice(11, 23);
                  } catch {
                    return "";
                  }
                })()}
              </span>
              {entry.labels?.pod && (
                <span class="min-w-[120px] shrink-0 text-status-info">
                  {entry.labels.pod.slice(0, 18)}
                </span>
              )}
              <span class="text-text-secondary break-all">{entry.line}</span>
            </div>
          );
        })}
      </div>
    </div>
  );
}
