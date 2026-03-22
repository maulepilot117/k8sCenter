import { useSignal } from "@preact/signals";
import { useEffect, useRef } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { getAccessToken } from "@/lib/api.ts";
import { Button } from "@/components/ui/Button.tsx";

interface PodTerminalProps {
  namespace: string;
  name: string;
  containers: string[];
}

interface Session {
  id: number;
  container: string;
  shell: string;
  // deno-lint-ignore no-explicit-any
  terminal: any;
  // deno-lint-ignore no-explicit-any
  fitAddon: any;
  ws: WebSocket | null;
  connected: boolean;
  error: string;
}

let sessionIdCounter = 0;

export default function PodTerminal(
  { namespace, name, containers }: PodTerminalProps,
) {
  const sessions = useSignal<Session[]>([]);
  const activeSessionId = useSignal<number | null>(null);
  const selectedContainer = useSignal(containers[0] || "");
  const fullscreen = useSignal(false);
  const containerRef = useRef<HTMLDivElement>(null);

  // Cleanup all sessions on unmount
  useEffect(() => {
    return () => {
      for (const session of sessions.value) {
        session.ws?.close();
        session.terminal?.dispose();
      }
    };
  }, []);

  // Handle Escape for fullscreen exit
  useEffect(() => {
    if (!IS_BROWSER || !fullscreen.value) return;
    const handler = (e: KeyboardEvent) => {
      if (e.key === "Escape") fullscreen.value = false;
    };
    globalThis.addEventListener("keydown", handler);
    return () => globalThis.removeEventListener("keydown", handler);
  }, [fullscreen.value]);

  // Refit terminal when fullscreen toggles
  useEffect(() => {
    if (!IS_BROWSER) return;
    const active = sessions.value.find((s) => s.id === activeSessionId.value);
    if (active?.fitAddon) {
      requestAnimationFrame(() => active.fitAddon.fit());
    }
  }, [fullscreen.value]);

  const openSession = async (container: string) => {
    if (!IS_BROWSER) return;

    const token = getAccessToken();
    if (!token) return;

    const id = ++sessionIdCounter;

    // Dynamic import to avoid SSR failures (xterm accesses DOM at import time)
    const { Terminal } = await import("@xterm/xterm");
    const { FitAddon } = await import("@xterm/addon-fit");
    const { WebLinksAddon } = await import("@xterm/addon-web-links");

    const terminal = new Terminal({
      cursorBlink: true,
      fontSize: 13,
      fontFamily: "'JetBrains Mono', 'Fira Code', Consolas, monospace",
      theme: { background: "#0f172a" },
    });
    const fitAddon = new FitAddon();
    terminal.loadAddon(fitAddon);
    terminal.loadAddon(new WebLinksAddon());

    const session: Session = {
      id,
      container,
      shell: "",
      terminal,
      fitAddon,
      ws: null,
      connected: false,
      error: "",
    };

    // Add session and activate it
    sessions.value = [...sessions.value, session];
    activeSessionId.value = id;

    // Wait a frame for the DOM to render the terminal container
    requestAnimationFrame(() => {
      const el = document.getElementById(`term-${id}`);
      if (!el) return;
      terminal.open(el);
      fitAddon.fit();

      // ResizeObserver for auto-fit
      const resizeObserver = new ResizeObserver(() => {
        clearTimeout(resizeTimer);
        resizeTimer = globalThis.setTimeout(() => fitAddon.fit(), 100);
      });
      let resizeTimer = 0;
      resizeObserver.observe(el);

      // Connect WebSocket
      const proto = globalThis.location.protocol === "https:" ? "wss:" : "ws:";
      const wsUrl =
        `${proto}//${globalThis.location.host}/ws/v1/ws/exec/${namespace}/${name}/${container}`;
      const ws = new WebSocket(wsUrl);

      ws.binaryType = "arraybuffer";

      ws.onopen = () => {
        updateSession(id, { ws, connected: true });
        terminal.focus();
      };

      ws.onmessage = (event) => {
        if (typeof event.data === "string") {
          // JSON control message from server
          try {
            const msg = JSON.parse(event.data);
            if (msg.type === "shell") {
              updateSession(id, { shell: msg.name });
            } else if (msg.type === "error") {
              updateSession(id, { error: msg.message });
              terminal.writeln(`\r\n\x1b[31mError: ${msg.message}\x1b[0m`);
            }
          } catch {
            // Plain text — write to terminal
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

      // Terminal input → WS (base64-encoded JSON)
      terminal.onData((data: string) => {
        if (ws.readyState === WebSocket.OPEN) {
          const encoded = btoa(data);
          ws.send(JSON.stringify({ type: "input", data: encoded }));
        }
      });

      // Terminal resize → WS
      terminal.onResize(({ cols, rows }: { cols: number; rows: number }) => {
        if (ws.readyState === WebSocket.OPEN) {
          ws.send(JSON.stringify({ type: "resize", cols, rows }));
        }
      });

      updateSession(id, { ws });
    });
  };

  const updateSession = (id: number, update: Partial<Session>) => {
    sessions.value = sessions.value.map((s) =>
      s.id === id ? { ...s, ...update } : s
    );
  };

  const closeSession = (id: number) => {
    const session = sessions.value.find((s) => s.id === id);
    if (session) {
      session.ws?.close();
      session.terminal?.dispose();
    }
    const remaining = sessions.value.filter((s) => s.id !== id);
    sessions.value = remaining;
    if (activeSessionId.value === id) {
      activeSessionId.value = remaining.length > 0
        ? remaining[remaining.length - 1].id
        : null;
    }
  };

  const reconnectSession = (id: number) => {
    const session = sessions.value.find((s) => s.id === id);
    if (!session) return;
    closeSession(id);
    openSession(session.container);
  };

  if (!IS_BROWSER) return null;

  return (
    <div
      ref={containerRef}
      class={fullscreen.value
        ? "fixed inset-0 z-50 flex flex-col bg-slate-900"
        : "flex flex-col gap-3"}
    >
      {/* Controls */}
      <div
        class={`flex items-center gap-3 ${
          fullscreen.value ? "bg-slate-800 px-4 py-2" : ""
        }`}
      >
        {/* Container picker + Open Terminal */}
        <select
          value={selectedContainer.value}
          onChange={(e) => {
            selectedContainer.value = (e.target as HTMLSelectElement).value;
          }}
          class="rounded-md border border-slate-300 dark:border-slate-600 bg-white dark:bg-slate-800 px-2 py-1 text-sm text-slate-900 dark:text-white"
        >
          {containers.map((c) => <option key={c} value={c}>{c}</option>)}
        </select>

        <Button
          variant="primary"
          size="sm"
          onClick={() => openSession(selectedContainer.value)}
        >
          Open Terminal
        </Button>

        {/* Fullscreen toggle */}
        <button
          type="button"
          onClick={() => {
            fullscreen.value = !fullscreen.value;
          }}
          class="ml-auto rounded p-1 text-slate-400 hover:bg-slate-100 hover:text-slate-600 dark:hover:bg-slate-700 dark:hover:text-slate-300"
          title={fullscreen.value ? "Exit fullscreen (Esc)" : "Fullscreen"}
        >
          <svg class="h-4 w-4" viewBox="0 0 16 16" fill="currentColor">
            {fullscreen.value
              ? (
                <>
                  <path d="M5 1v4H1v1h5V1H5z" />
                  <path d="M10 1v5h5V5h-4V1h-1z" />
                  <path d="M1 10v1h4v4h1v-5H1z" />
                  <path d="M10 10v5h1v-4h4v-1h-5z" />
                </>
              )
              : (
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
      {sessions.value.length > 0 && (
        <div
          class={`flex items-center gap-1 border-b border-slate-200 dark:border-slate-700 ${
            fullscreen.value ? "px-4" : ""
          }`}
        >
          {sessions.value.map((s) => (
            <div key={s.id} class="flex items-center">
              <button
                type="button"
                onClick={() => {
                  activeSessionId.value = s.id;
                }}
                class={`px-3 py-1.5 text-sm font-medium border-b-2 transition-colors ${
                  activeSessionId.value === s.id
                    ? "border-brand text-brand"
                    : "border-transparent text-slate-500 hover:text-slate-700 dark:hover:text-slate-300"
                }`}
              >
                {s.container}
                {s.shell ? ` (${s.shell.split("/").pop()})` : ""}
                <span
                  class={`ml-1.5 inline-block h-1.5 w-1.5 rounded-full ${
                    s.connected ? "bg-green-500" : "bg-slate-400"
                  }`}
                />
              </button>
              <button
                type="button"
                onClick={() => closeSession(s.id)}
                class="ml-0.5 rounded p-0.5 text-slate-400 hover:bg-slate-200 hover:text-slate-600 dark:hover:bg-slate-700"
                title="Close session"
              >
                <svg class="h-3 w-3" viewBox="0 0 12 12" fill="currentColor">
                  <path d="M3.172 3.172a.5.5 0 01.707 0L6 5.293l2.121-2.121a.5.5 0 01.707.707L6.707 6l2.121 2.121a.5.5 0 01-.707.707L6 6.707 3.879 8.828a.5.5 0 01-.707-.707L5.293 6 3.172 3.879a.5.5 0 010-.707z" />
                </svg>
              </button>
            </div>
          ))}
        </div>
      )}

      {/* Terminal area */}
      <div class={`relative ${fullscreen.value ? "flex-1 px-4 pb-4" : ""}`}>
        {sessions.value.length === 0
          ? (
            <div
              class={`flex items-center justify-center rounded-lg bg-slate-900 text-slate-500 ${
                fullscreen.value ? "h-full" : "h-96"
              }`}
            >
              Select a container and click "Open Terminal" to start
            </div>
          )
          : (
            sessions.value.map((s) => (
              <div
                key={s.id}
                class={activeSessionId.value === s.id ? "block" : "hidden"}
                style={{
                  height: fullscreen.value ? "100%" : "500px",
                }}
              >
                <div
                  id={`term-${s.id}`}
                  class="h-full w-full"
                />
                {/* Reconnect overlay */}
                {!s.connected && s.terminal && (
                  <div class="absolute inset-0 flex items-center justify-center bg-black/60">
                    <div class="text-center">
                      <p class="mb-3 text-sm text-slate-300">
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
