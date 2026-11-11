import { useCallback, useEffect, useRef, useState } from "react";
import { Button } from "@/components/ui/Button.tsx";
import { getAccessToken, readyAccessToken } from "@/lib/api.ts";
import { streamAuthFrame, streamErrorMessage } from "@/lib/stream-state.ts";
import { type ClusterTarget, currentTarget } from "@/src/lib/cluster.ts";
import { useIsClient } from "@/src/lib/use-is-client.ts";
import { useStateRef } from "@/src/lib/use-state-ref.ts";

interface PodTerminalProps {
  namespace: string;
  name: string;
  containers: string[];
}

interface Session {
  id: number;
  container: string;
  /** The cluster the session was opened for; its socket names it. */
  target: ClusterTarget;
  shell: string;
  terminal: any;
  fitAddon: any;
  ws: WebSocket | null;
  connected: boolean;
  error: string;
}

let sessionIdCounter = 0;

export default function PodTerminal({
  namespace,
  name,
  containers,
}: PodTerminalProps) {
  const isClient = useIsClient();
  // Socket callbacks, ref callbacks and rAF callbacks outlive the render that
  // created them, so they read the live lists through the refs the setters
  // write first.
  const [sessions, commitSessions, sessionsRef] = useStateRef<Session[]>([]);
  const [activeSessionId, commitActiveSessionId, activeSessionIdRef] =
    useStateRef<number | null>(null);
  const [selectedContainer, setSelectedContainer] = useState(
    containers[0] || "",
  );
  const [fullscreen, setFullscreen] = useState(false);
  const containerRef = useRef<HTMLDivElement>(null);

  const updateSession = useCallback((id: number, update: Partial<Session>) => {
    commitSessions(
      sessionsRef.current.map((s) => (s.id === id ? { ...s, ...update } : s)),
    );
  }, []);

  // Ref callback registry: maps session id -> DOM element
  const terminalRefs = useRef<Map<number, HTMLDivElement>>(new Map());
  // Track which sessions have been initialized (idempotency guard)
  const initializedSessions = useRef<Set<number>>(new Set());
  // Track pending async opens for cancellation
  const pendingOpens = useRef<Map<number, { canceled: boolean }>>(new Map());
  // Track ResizeObservers for cleanup
  const resizeObservers = useRef<Map<number, ResizeObserver>>(new Map());

  // Cleanup all sessions on unmount
  useEffect(() => {
    return () => {
      for (const session of sessionsRef.current) {
        session.ws?.close();
        session.terminal?.dispose();
      }
      // Cancel any pending opens
      for (const token of pendingOpens.current.values()) {
        token.canceled = true;
      }
      // Disconnect all resize observers
      for (const observer of resizeObservers.current.values()) {
        observer.disconnect();
      }
      terminalRefs.current.clear();
      initializedSessions.current.clear();
      pendingOpens.current.clear();
      resizeObservers.current.clear();
    };
  }, []);

  // Handle Escape for fullscreen exit
  useEffect(() => {
    if (!fullscreen) return;
    const handler = (e: KeyboardEvent) => {
      if (e.key === "Escape") setFullscreen(false);
    };
    globalThis.addEventListener("keydown", handler);
    return () => globalThis.removeEventListener("keydown", handler);
  }, [fullscreen]);

  // Refit terminal when fullscreen toggles
  useEffect(() => {
    // Runs on mount and whenever fullscreen toggles.
    void fullscreen;
    const active = sessionsRef.current.find(
      (s) => s.id === activeSessionIdRef.current,
    );
    if (active?.fitAddon) {
      requestAnimationFrame(() => active.fitAddon.fit());
    }
  }, [fullscreen]);

  // Initialize terminal when ref callback fires for an active, uninitialized session
  const initTerminalInDom = useCallback(
    (id: number, el: HTMLDivElement) => {
      if (initializedSessions.current.has(id)) return;

      const session = sessionsRef.current.find((s) => s.id === id);
      if (!session?.terminal || !session?.fitAddon) return;

      // Only init if the element is visible (active tab)
      if (el.offsetWidth === 0 || el.offsetHeight === 0) return;

      initializedSessions.current.add(id);

      const { terminal, fitAddon } = session;
      terminal.open(el);
      fitAddon.fit();

      // ResizeObserver for auto-fit — declare timer before closure
      let resizeTimer: ReturnType<typeof setTimeout> | undefined;
      const resizeObserver = new ResizeObserver(() => {
        clearTimeout(resizeTimer);
        resizeTimer = globalThis.setTimeout(() => fitAddon.fit(), 100);
      });
      resizeObserver.observe(el);
      resizeObservers.current.set(id, resizeObserver);

      // Connect WebSocket
      const token = getAccessToken();
      if (!token) return;

      const proto = globalThis.location.protocol === "https:" ? "wss:" : "ws:";
      // Exec authenticates in-band like every other stream: the token and
      // the session's cluster go in the first frame, never in the URL (an
      // ingress in front of this pod logs request URLs by default).
      const wsUrl = `${proto}//${globalThis.location.host}/ws/v1/ws/exec/${namespace}/${name}/${session.container}`;
      const ws = new WebSocket(wsUrl);

      ws.binaryType = "arraybuffer";

      ws.onopen = () => {
        ws.send(JSON.stringify(streamAuthFrame(token, session.target)));
        updateSession(id, { ws, connected: true });
        terminal.focus();
      };

      ws.onmessage = (event) => {
        if (typeof event.data === "string") {
          try {
            const msg = JSON.parse(event.data);
            if (msg.type === "shell") {
              updateSession(id, { shell: msg.name });
            } else if (msg.type === "error") {
              const message = streamErrorMessage(msg, "Terminal");
              updateSession(id, { error: message });
              terminal.writeln(`\r\n\x1b[31mError: ${message}\x1b[0m`);
            }
          } catch {
            terminal.write(event.data);
          }
        } else if (event.data instanceof ArrayBuffer) {
          terminal.write(new Uint8Array(event.data));
        }
      };

      ws.onclose = () => {
        updateSession(id, { connected: false, ws: null });
        terminal.writeln("\r\n\x1b[33mSession ended.\x1b[0m");
      };

      ws.onerror = () => {
        updateSession(id, {
          error: "WebSocket connection failed",
          connected: false,
        });
      };

      // Terminal input -> WS (base64-encoded JSON)
      terminal.onData((data: string) => {
        if (ws.readyState === WebSocket.OPEN) {
          const encoded = btoa(data);
          ws.send(JSON.stringify({ type: "input", data: encoded }));
        }
      });

      // Terminal resize -> WS
      terminal.onResize(({ cols, rows }: { cols: number; rows: number }) => {
        if (ws.readyState === WebSocket.OPEN) {
          ws.send(JSON.stringify({ type: "resize", cols, rows }));
        }
      });

      updateSession(id, { ws });
    },
    [namespace, name, updateSession],
  );

  // Ref callback for terminal container divs
  const termRefCallback = useCallback(
    (id: number) => (el: HTMLDivElement | null) => {
      if (el) {
        terminalRefs.current.set(id, el);
        // Try to initialize immediately if session data is ready
        initTerminalInDom(id, el);
      } else {
        terminalRefs.current.delete(id);
      }
    },
    [initTerminalInDom],
  );

  // When active session changes, try to init any session that was deferred
  // because it was hidden (0x0) when its ref first mounted
  useEffect(() => {
    if (activeSessionId === null) return;
    const id = activeSessionId;
    if (initializedSessions.current.has(id)) {
      // Already initialized — just refit
      const session = sessionsRef.current.find((s) => s.id === id);
      if (session?.fitAddon) {
        requestAnimationFrame(() => session.fitAddon.fit());
      }
      return;
    }
    const el = terminalRefs.current.get(id);
    if (el) {
      // Element exists but wasn't initialized (was hidden). Use rAF to
      // allow the browser to apply display:block first.
      requestAnimationFrame(() => initTerminalInDom(id, el));
    }
  }, [activeSessionId, initTerminalInDom]);

  const openSession = async (container: string) => {
    // Taken once, before any await: the session stays on the cluster
    // selected when it was opened.
    const target = currentTarget();
    const id = ++sessionIdCounter;

    // Register cancellation token before any async work, the token wait
    // included, so an unmount meanwhile cancels this open.
    const cancelToken = { canceled: false };
    pendingOpens.current.set(id, cancelToken);

    const token = getAccessToken() ?? (await readyAccessToken());
    if (!token || cancelToken.canceled) {
      pendingOpens.current.delete(id);
      return;
    }

    // Dynamic import to avoid SSR failures (xterm accesses DOM at import time)
    const { Terminal } = await import("@xterm/xterm");
    if (cancelToken.canceled) return;

    const { FitAddon } = await import("@xterm/addon-fit");
    if (cancelToken.canceled) return;

    const { WebLinksAddon } = await import("@xterm/addon-web-links");
    if (cancelToken.canceled) return;

    // Imports complete — remove from pending
    pendingOpens.current.delete(id);

    const terminal = new Terminal({
      cursorBlink: true,
      fontSize: 13,
      fontFamily: "'JetBrains Mono', 'Fira Code', Consolas, monospace",
      // xterm's canvas renderer can't resolve CSS vars — hex must match
      // bgSurface in shared/themes/liquid-glass.json.
      theme: { background: "#0d1422" },
    });
    const fitAddon = new FitAddon();
    terminal.loadAddon(fitAddon);
    terminal.loadAddon(new WebLinksAddon());

    const session: Session = {
      id,
      container,
      target,
      shell: "",
      terminal,
      fitAddon,
      ws: null,
      connected: false,
      error: "",
    };

    // Add session and activate it — ref callback will handle DOM init
    commitSessions([...sessionsRef.current, session]);
    commitActiveSessionId(id);
  };

  const closeSession = (id: number) => {
    // Cancel any pending async open for this id
    const pendingToken = pendingOpens.current.get(id);
    if (pendingToken) {
      pendingToken.canceled = true;
      pendingOpens.current.delete(id);
    }

    const session = sessionsRef.current.find((s) => s.id === id);
    if (session) {
      session.ws?.close();
      session.terminal?.dispose();
    }

    // Disconnect and remove ResizeObserver
    const observer = resizeObservers.current.get(id);
    if (observer) {
      observer.disconnect();
      resizeObservers.current.delete(id);
    }

    // Clean up refs
    terminalRefs.current.delete(id);
    initializedSessions.current.delete(id);

    const remaining = sessionsRef.current.filter((s) => s.id !== id);
    commitSessions(remaining);
    if (activeSessionIdRef.current === id) {
      commitActiveSessionId(
        remaining.length > 0 ? remaining[remaining.length - 1].id : null,
      );
    }
  };

  const reconnectSession = (id: number) => {
    const session = sessionsRef.current.find((s) => s.id === id);
    if (!session) return;
    closeSession(id);
    openSession(session.container);
  };

  if (!isClient) return null;

  return (
    <div
      ref={containerRef}
      className={
        fullscreen
          ? "fixed inset-0 z-50 flex flex-col bg-base"
          : "flex flex-col gap-3"
      }
    >
      {/* Controls */}
      <div
        className={`flex items-center gap-3 ${
          fullscreen ? "bg-surface px-4 py-2" : ""
        }`}
      >
        {/* Container picker + Open Terminal */}
        <select
          value={selectedContainer}
          onChange={(e) => {
            setSelectedContainer(e.currentTarget.value);
          }}
          className="rounded-md border border-border-primary bg-surface px-2 py-1 text-sm text-text-primary"
        >
          {containers.map((c) => (
            <option key={c} value={c}>
              {c}
            </option>
          ))}
        </select>

        <Button
          variant="primary"
          size="sm"
          onClick={() => openSession(selectedContainer)}
        >
          Open Terminal
        </Button>

        {/* Fullscreen toggle */}
        <button
          type="button"
          onClick={() => {
            setFullscreen((prev) => !prev);
          }}
          className="ml-auto rounded p-1 text-text-muted hover:bg-elevated hover:text-text-secondary"
          title={fullscreen ? "Exit fullscreen (Esc)" : "Fullscreen"}
        >
          <svg className="h-4 w-4" viewBox="0 0 16 16" fill="currentColor">
            {fullscreen ? (
              <>
                <path d="M5 1v4H1v1h5V1H5z" />
                <path d="M10 1v5h5V5h-4V1h-1z" />
                <path d="M1 10v1h4v4h1v-5H1z" />
                <path d="M10 10v5h1v-4h4v-1h-5z" />
              </>
            ) : (
              <>
                <path d="M1 1v5h1V2.707L5.293 6 6 5.293 2.707 2H6V1H1z" />
                <path d="M10 1v1h3.293L10 5.293l.707.707L14 2.707V6h1V1h-5z" />
                <path d="M1 10v5h5v-1H2.707L6 10.707 5.293 10 2 13.293V10H1z" />
                <path d="M14 10v3.293L10.707 10 10 10.707 13.293 14H10v1h5v-5h-1z" />
              </>
            )}
          </svg>
        </button>
      </div>

      {/* Session tabs */}
      {sessions.length > 0 && (
        <div
          className={`flex items-center gap-1 border-b border-border-primary ${
            fullscreen ? "px-4" : ""
          }`}
        >
          {sessions.map((s) => (
            <div key={s.id} className="flex items-center">
              <button
                type="button"
                onClick={() => {
                  commitActiveSessionId(s.id);
                }}
                className={`px-3 py-1.5 text-sm font-medium border-b-2 transition-colors ${
                  activeSessionId === s.id
                    ? "border-brand text-brand"
                    : "border-transparent text-text-muted hover:text-text-primary"
                }`}
              >
                {s.container}
                {s.shell ? ` (${s.shell.split("/").pop()})` : ""}
                <span
                  className={`ml-1.5 inline-block h-1.5 w-1.5 rounded-full ${
                    s.connected ? "bg-success" : "bg-text-muted"
                  }`}
                />
              </button>
              <button
                type="button"
                onClick={() => closeSession(s.id)}
                className="ml-0.5 rounded p-0.5 text-text-muted hover:bg-hover hover:text-text-secondary"
                title="Close session"
              >
                <svg
                  className="h-3 w-3"
                  viewBox="0 0 12 12"
                  fill="currentColor"
                >
                  <path d="M3.172 3.172a.5.5 0 01.707 0L6 5.293l2.121-2.121a.5.5 0 01.707.707L6.707 6l2.121 2.121a.5.5 0 01-.707.707L6 6.707 3.879 8.828a.5.5 0 01-.707-.707L5.293 6 3.172 3.879a.5.5 0 010-.707z" />
                </svg>
              </button>
            </div>
          ))}
        </div>
      )}

      {/* Terminal area */}
      <div className={`relative ${fullscreen ? "flex-1 px-4 pb-4" : ""}`}>
        {sessions.length === 0 ? (
          <div
            className={`flex items-center justify-center rounded-lg bg-base text-text-muted ${
              fullscreen ? "h-full" : "h-96"
            }`}
          >
            Select a container and click "Open Terminal" to start
          </div>
        ) : (
          sessions.map((s) => (
            <div
              key={s.id}
              className={activeSessionId === s.id ? "block" : "hidden"}
              style={{
                height: fullscreen ? "100%" : "500px",
              }}
            >
              <div ref={termRefCallback(s.id)} className="h-full w-full" />
              {/* Reconnect overlay */}
              {!s.connected && s.terminal && (
                <div className="absolute inset-0 flex items-center justify-center glass-scrim">
                  <div className="text-center">
                    <p className="mb-3 text-sm text-text-secondary">
                      {s.error || "Session ended"}
                    </p>
                    <Button
                      variant="primary"
                      size="sm"
                      onClick={() => reconnectSession(s.id)}
                    >
                      Reconnect
                    </Button>
                  </div>
                </div>
              )}
            </div>
          ))
        )}
      </div>
    </div>
  );
}
