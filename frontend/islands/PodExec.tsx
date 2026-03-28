import { useSignal } from "@preact/signals";
import { useEffect, useRef } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { getAccessToken } from "@/lib/api.ts";
import { Button } from "@/components/ui/Button.tsx";

interface PodExecProps {
  namespace: string;
  name: string;
  containers: string[];
}

export default function PodExec(
  { namespace, name, containers }: PodExecProps,
) {
  const container = useSignal(containers[0] || "");
  const connected = useSignal(false);
  const error = useSignal<string | null>(null);
  const termRef = useRef<HTMLDivElement>(null);
  const wsRef = useRef<WebSocket | null>(null);

  useEffect(() => {
    return () => {
      wsRef.current?.close();
    };
  }, []);

  if (!IS_BROWSER) return null;

  const connect = () => {
    if (wsRef.current) {
      wsRef.current.close();
    }
    error.value = null;

    const token = getAccessToken();
    if (!token) {
      error.value = "Not authenticated";
      return;
    }

    const proto = globalThis.location.protocol === "https:" ? "wss:" : "ws:";
    const wsUrl =
      `${proto}//${globalThis.location.host}/ws/v1/ws/exec/${namespace}/${name}/${container.value}`;

    const ws = new WebSocket(wsUrl, ["v4.channel.k8s.io"]);
    wsRef.current = ws;

    const term = termRef.current;
    if (term) term.textContent = "";

    ws.onopen = () => {
      connected.value = true;
      // Send auth token as first message
      ws.send(JSON.stringify({ token }));
    };

    ws.onmessage = (event) => {
      if (term) {
        if (typeof event.data === "string") {
          term.textContent += event.data;
        } else if (event.data instanceof Blob) {
          event.data.text().then((text: string) => {
            if (term) term.textContent += text;
          });
        }
        term.scrollTop = term.scrollHeight;
      }
    };

    ws.onclose = () => {
      connected.value = false;
    };

    ws.onerror = () => {
      error.value = "WebSocket connection failed";
      connected.value = false;
    };
  };

  const disconnect = () => {
    wsRef.current?.close();
    wsRef.current = null;
    connected.value = false;
  };

  const handleKeyDown = (e: KeyboardEvent) => {
    if (!wsRef.current || wsRef.current.readyState !== WebSocket.OPEN) return;

    let data = "";
    if (e.key.length === 1) {
      data = e.key;
    } else if (e.key === "Enter") {
      data = "\r";
    } else if (e.key === "Backspace") {
      data = "\x7f";
    } else if (e.key === "Tab") {
      e.preventDefault();
      data = "\t";
    } else if (e.ctrlKey && e.key === "c") {
      data = "\x03";
    } else if (e.ctrlKey && e.key === "d") {
      data = "\x04";
    }

    if (data) {
      wsRef.current.send(data);
    }
  };

  return (
    <div class="space-y-3">
      <div class="flex items-center gap-3">
        {containers.length > 1 && (
          <select
            value={container.value}
            onChange={(e) =>
              container.value = (e.target as HTMLSelectElement).value}
            disabled={connected.value}
            class="rounded-md border border-border-primary bg-surface px-2 py-1 text-sm text-text-primary"
          >
            {containers.map((c) => <option key={c} value={c}>{c}</option>)}
          </select>
        )}
        {!connected.value
          ? (
            <Button variant="primary" size="sm" onClick={connect}>
              Connect
            </Button>
          )
          : (
            <Button variant="danger" size="sm" onClick={disconnect}>
              Disconnect
            </Button>
          )}
        <span
          class={`text-xs ${
            connected.value ? "text-success" : "text-text-muted"
          }`}
        >
          {connected.value ? "Connected" : "Disconnected"}
        </span>
      </div>

      {error.value && <p class="text-sm text-danger">{error.value}</p>}

      <div
        ref={termRef}
        tabIndex={0}
        onKeyDown={handleKeyDown}
        class="bg-base text-success font-mono text-sm p-4 rounded-md h-96 overflow-y-auto whitespace-pre-wrap focus:outline-none focus:ring-2 focus:ring-accent cursor-text"
        style="min-height: 24rem;"
      >
        {!connected.value && (
          <span class="text-text-muted">
            Click"Connect" to start an exec session
          </span>
        )}
      </div>
    </div>
  );
}
