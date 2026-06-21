import { useSignal } from "@preact/signals";
import { useEffect, useRef } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import type { JSX } from "preact";
import { apiGet, getAccessToken } from "@/lib/api.ts";
import { useNamespaces } from "@/lib/hooks/use-namespaces.ts";
import { initialNamespace } from "@/lib/namespace.ts";
import { Button } from "@/components/ui/Button.tsx";

interface FlowRecord {
  time: string;
  verdict: string;
  dropReason?: string;
  direction: string;
  srcNamespace: string;
  srcPod: string;
  srcIP?: string;
  srcLabels?: string[];
  srcNames?: string[];
  srcService?: string;
  dstNamespace: string;
  dstPod: string;
  dstIP?: string;
  dstLabels?: string[];
  dstNames?: string[];
  dstService?: string;
  protocol: string;
  dstPort?: number;
  srcPort?: number;
  summary?: string;
}

/** Format an endpoint for display — shows pod, IP, DNS name, or identity label */
function formatEndpoint(
  ns: string,
  pod: string,
  ip?: string,
  names?: string[],
  service?: string,
  labels?: string[],
): { primary: string; detail?: string } {
  if (pod) {
    return {
      primary: ns ? `${ns}/${pod}` : pod,
      detail: service || undefined,
    };
  }
  // External traffic — no pod
  const dnsName = names?.length ? names[0] : undefined;
  const identity = labels?.find((l) => l.startsWith("reserved:"))?.replace(
    "reserved:",
    "",
  );
  if (dnsName) {
    return { primary: dnsName, detail: ip };
  }
  if (ip) {
    return { primary: ip, detail: identity };
  }
  return { primary: identity ?? "unknown" };
}

const VERDICT_OPTIONS = ["", "FORWARDED", "DROPPED", "ERROR", "AUDIT"];
const MAX_FLOWS = 1000;

function verdictStyle(verdict: string): JSX.CSSProperties {
  switch (verdict) {
    case "FORWARDED":
      return {
        background: "color-mix(in srgb, var(--success) 15%, transparent)",
        color: "var(--success)",
      };
    case "DROPPED":
    case "ERROR":
      return {
        background: "color-mix(in srgb, var(--error) 15%, transparent)",
        color: "var(--error)",
      };
    case "AUDIT":
      return {
        background: "color-mix(in srgb, var(--warning) 15%, transparent)",
        color: "var(--warning)",
      };
    default:
      return {
        background: "var(--bg-elevated)",
        color: "var(--text-muted)",
      };
  }
}

export default function FlowViewer() {
  const namespace = useSignal(initialNamespace());
  const namespaces = useNamespaces();
  const verdict = useSignal("");
  const flows = useSignal<FlowRecord[]>([]);
  const loading = useSignal(false);
  const error = useSignal<string | null>(null);
  const wsState = useSignal<"disconnected" | "connecting" | "live">(
    "disconnected",
  );

  // Connection generation counter — prevents stale WS callbacks from clobbering state
  const wsIdRef = useRef(0);
  const wsRef = useRef<WebSocket | null>(null);

  // RAF batching for high-volume flow updates
  const pendingFlows = useRef<FlowRecord[]>([]);
  const rafId = useRef<number>(0);

  const flushFlows = () => {
    rafId.current = 0;
    const batch = pendingFlows.current;
    if (batch.length === 0) return;
    pendingFlows.current = [];
    const current = flows.value;
    const merged = [...batch.reverse(), ...current];
    flows.value = merged.length > MAX_FLOWS
      ? merged.slice(0, MAX_FLOWS)
      : merged;
  };

  // HTTP fallback fetch
  const fetchFlows = () => {
    if (!IS_BROWSER) return;
    loading.value = true;
    error.value = null;
    let url = `/v1/networking/hubble/flows?namespace=${
      encodeURIComponent(namespace.value)
    }&limit=200`;
    if (verdict.value) {
      url += `&verdict=${encodeURIComponent(verdict.value)}`;
    }
    apiGet<FlowRecord[]>(url)
      .then((resp) => {
        flows.value = Array.isArray(resp.data) ? resp.data : [];
      })
      .catch((err: unknown) => {
        const msg = err instanceof Error
          ? err.message
          : "Failed to fetch flows";
        error.value = msg;
        flows.value = [];
      })
      .finally(() => {
        loading.value = false;
      });
  };

  // Connect WebSocket for live streaming
  const connectWS = () => {
    // Close existing connection
    if (wsRef.current) {
      wsRef.current.close();
      wsRef.current = null;
    }

    const token = getAccessToken();
    if (!token) {
      fetchFlows();
      return;
    }

    // Increment generation — all callbacks check staleness against this
    const connectionId = ++wsIdRef.current;
    const isStale = () => wsIdRef.current !== connectionId;

    wsState.value = "connecting";

    const proto = globalThis.location.protocol === "https:" ? "wss:" : "ws:";
    const ws = new WebSocket(
      `${proto}//${globalThis.location.host}/ws/v1/ws/flows`,
    );
    wsRef.current = ws;

    ws.onopen = () => {
      if (isStale()) {
        ws.close();
        return;
      }
      ws.send(JSON.stringify({ type: "auth", token }));
      ws.send(
        JSON.stringify({
          namespace: namespace.value,
          verdict: verdict.value,
        }),
      );
    };

    ws.onmessage = (event) => {
      if (isStale()) return;
      try {
        const msg = JSON.parse(event.data);
        if (msg.type === "flow" && msg.data) {
          // Batch flows for RAF flush
          pendingFlows.current.push(msg.data);
          if (!rafId.current) {
            rafId.current = requestAnimationFrame(flushFlows);
          }
        } else if (msg.type === "subscribed") {
          wsState.value = "live";
          error.value = null;
          flows.value = [];
        } else if (msg.type === "error") {
          error.value = msg.message;
          wsState.value = "disconnected";
        }
      } catch {
        // Ignore malformed messages
      }
    };

    ws.onclose = () => {
      if (isStale()) return;
      wsState.value = "disconnected";
      wsRef.current = null;
    };

    ws.onerror = () => {
      if (isStale()) return;
      wsState.value = "disconnected";
      wsRef.current = null;
      fetchFlows();
    };
  };

  // Connect on mount and when filters change
  useEffect(() => {
    if (!IS_BROWSER) return;
    connectWS();
    return () => {
      // Increment generation so any in-flight callbacks become stale
      wsIdRef.current++;
      if (wsRef.current) {
        wsRef.current.close();
        wsRef.current = null;
      }
      wsState.value = "disconnected";
      if (rafId.current) {
        cancelAnimationFrame(rafId.current);
        rafId.current = 0;
      }
      pendingFlows.current = [];
    };
  }, [namespace.value, verdict.value]);

  if (!IS_BROWSER) {
    return (
      <div style={{ padding: "24px" }}>
        <h1
          style={{
            fontSize: "24px",
            fontWeight: 700,
            color: "var(--text-primary)",
          }}
        >
          Network Flows
        </h1>
      </div>
    );
  }

  return (
    <div style={{ padding: "24px" }}>
      <div
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          marginBottom: "24px",
        }}
      >
        <div style={{ display: "flex", alignItems: "center", gap: "12px" }}>
          <h1
            style={{
              margin: 0,
              fontSize: "24px",
              fontWeight: 700,
              color: "var(--text-primary)",
            }}
          >
            Network Flows
          </h1>
          {wsState.value === "live" && (
            <span
              style={{
                display: "inline-flex",
                alignItems: "center",
                gap: "6px",
                padding: "3px 10px",
                borderRadius: "12px",
                fontSize: "11px",
                fontWeight: 600,
                background:
                  "color-mix(in srgb, var(--success) 15%, transparent)",
                color: "var(--success)",
              }}
            >
              <span
                style={{
                  width: "6px",
                  height: "6px",
                  borderRadius: "50%",
                  background: "var(--success)",
                  animation: "pulse 2s infinite",
                }}
              />
              Live
            </span>
          )}
          {wsState.value === "connecting" && (
            <span
              style={{
                display: "inline-flex",
                alignItems: "center",
                gap: "6px",
                padding: "3px 10px",
                borderRadius: "12px",
                fontSize: "11px",
                fontWeight: 600,
                background:
                  "color-mix(in srgb, var(--warning) 15%, transparent)",
                color: "var(--warning)",
              }}
            >
              Connecting...
            </span>
          )}
        </div>
        <Button
          variant="secondary"
          onClick={fetchFlows}
          disabled={loading.value}
        >
          {loading.value ? "Loading..." : "Refresh"}
        </Button>
      </div>

      {/* Filters */}
      <div
        style={{
          display: "flex",
          alignItems: "center",
          gap: "16px",
          marginBottom: "16px",
        }}
      >
        <div>
          <label
            style={{
              display: "block",
              fontSize: "11px",
              fontWeight: 600,
              textTransform: "uppercase",
              letterSpacing: "0.07em",
              color: "var(--text-muted)",
              marginBottom: "4px",
            }}
          >
            Namespace
          </label>
          <select
            value={namespace.value}
            onChange={(e) =>
              namespace.value = (e.target as HTMLSelectElement).value}
            style={{
              borderRadius: "9px",
              border: "1px solid var(--border-primary)",
              background: "var(--bg-surface)",
              padding: "6px 12px",
              fontSize: "13px",
              color: "var(--text-primary)",
              fontFamily: "inherit",
            }}
          >
            {namespaces.value.map((ns) => (
              <option key={ns} value={ns}>{ns}</option>
            ))}
          </select>
        </div>
        <div>
          <label
            style={{
              display: "block",
              fontSize: "11px",
              fontWeight: 600,
              textTransform: "uppercase",
              letterSpacing: "0.07em",
              color: "var(--text-muted)",
              marginBottom: "4px",
            }}
          >
            Verdict
          </label>
          <select
            value={verdict.value}
            onChange={(e) =>
              verdict.value = (e.target as HTMLSelectElement).value}
            style={{
              borderRadius: "9px",
              border: "1px solid var(--border-primary)",
              background: "var(--bg-surface)",
              padding: "6px 12px",
              fontSize: "13px",
              color: "var(--text-primary)",
              fontFamily: "inherit",
            }}
          >
            {VERDICT_OPTIONS.map((v) => (
              <option key={v} value={v}>{v || "All"}</option>
            ))}
          </select>
        </div>
        <div
          style={{
            fontSize: "12px",
            color: "var(--text-muted)",
            alignSelf: "flex-end",
          }}
        >
          {flows.value.length} flows
        </div>
      </div>

      {error.value && (
        <div
          style={{
            marginBottom: "16px",
            borderRadius: "9px",
            background: "color-mix(in srgb, var(--error) 12%, transparent)",
            border:
              "1px solid color-mix(in srgb, var(--error) 30%, transparent)",
            padding: "12px 16px",
          }}
        >
          <p style={{ fontSize: "13px", color: "var(--error)", margin: 0 }}>
            {error.value}
          </p>
        </div>
      )}

      {/* Flow table — solid surface */}
      <div
        style={{
          overflowX: "auto",
          borderRadius: "9px",
          border: "1px solid var(--border-primary)",
        }}
      >
        <table
          style={{
            minWidth: "100%",
            fontSize: "13px",
            borderCollapse: "collapse",
          }}
        >
          <thead>
            <tr style={{ background: "var(--bg-surface)", textAlign: "left" }}>
              <th
                style={{
                  padding: "10px 12px",
                  fontSize: "11px",
                  fontWeight: 600,
                  textTransform: "uppercase",
                  letterSpacing: "0.07em",
                  color: "var(--text-muted)",
                }}
              >
                Time
              </th>
              <th
                style={{
                  padding: "10px 12px",
                  fontSize: "11px",
                  fontWeight: 600,
                  textTransform: "uppercase",
                  letterSpacing: "0.07em",
                  color: "var(--text-muted)",
                }}
              >
                Direction
              </th>
              <th
                style={{
                  padding: "10px 12px",
                  fontSize: "11px",
                  fontWeight: 600,
                  textTransform: "uppercase",
                  letterSpacing: "0.07em",
                  color: "var(--text-muted)",
                }}
              >
                Source
              </th>
              <th
                style={{
                  padding: "10px 12px",
                  fontSize: "11px",
                  fontWeight: 600,
                  textTransform: "uppercase",
                  letterSpacing: "0.07em",
                  color: "var(--text-muted)",
                }}
              >
                Destination
              </th>
              <th
                style={{
                  padding: "10px 12px",
                  fontSize: "11px",
                  fontWeight: 600,
                  textTransform: "uppercase",
                  letterSpacing: "0.07em",
                  color: "var(--text-muted)",
                }}
              >
                Protocol
              </th>
              <th
                style={{
                  padding: "10px 12px",
                  fontSize: "11px",
                  fontWeight: 600,
                  textTransform: "uppercase",
                  letterSpacing: "0.07em",
                  color: "var(--text-muted)",
                }}
              >
                Verdict
              </th>
            </tr>
          </thead>
          <tbody>
            {flows.value.length === 0 && !loading.value && (
              <tr>
                <td
                  colSpan={6}
                  style={{
                    padding: "48px 12px",
                    textAlign: "center",
                    color: "var(--text-muted)",
                    fontSize: "13px",
                  }}
                >
                  {error.value
                    ? "Failed to load flows"
                    : wsState.value === "live"
                    ? "Waiting for flows..."
                    : "No flows found. Hubble may not be enabled or there is no traffic in this namespace."}
                </td>
              </tr>
            )}
            {flows.value.map((f, i) => (
              <FlowRow
                key={`${f.time}-${i}`}
                f={f}
              />
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function FlowRow({ f }: { f: FlowRecord }) {
  const hovered = useSignal(false);

  const src = formatEndpoint(
    f.srcNamespace,
    f.srcPod,
    f.srcIP,
    f.srcNames,
    f.srcService,
    f.srcLabels,
  );
  const dst = formatEndpoint(
    f.dstNamespace,
    f.dstPod,
    f.dstIP,
    f.dstNames,
    f.dstService,
    f.dstLabels,
  );

  return (
    <tr
      style={{
        borderTop: "1px solid var(--border-subtle)",
        background: hovered.value
          ? "color-mix(in srgb, var(--accent) 5%, transparent)"
          : "transparent",
        transition: "background 0.1s ease",
      }}
      onMouseEnter={() => {
        hovered.value = true;
      }}
      onMouseLeave={() => {
        hovered.value = false;
      }}
    >
      <td
        style={{
          padding: "6px 12px",
          color: "var(--text-muted)",
          fontFamily: "var(--font-mono)",
          fontSize: "12px",
          whiteSpace: "nowrap",
          fontVariantNumeric: "tabular-nums",
        }}
      >
        {formatTime(f.time)}
      </td>
      <td
        style={{
          padding: "6px 12px",
          fontSize: "12px",
          color: "var(--text-muted)",
        }}
      >
        {f.direction === "INGRESS" ? "⬇" : "⬆"} {f.direction}
      </td>
      <td style={{ padding: "6px 12px", whiteSpace: "nowrap" }}>
        <div>
          <span style={{ color: "var(--text-primary)" }}>{src.primary}</span>
          {src.detail && (
            <span
              style={{
                marginLeft: "4px",
                fontSize: "11px",
                color: "var(--text-muted)",
              }}
            >
              {src.detail}
            </span>
          )}
        </div>
      </td>
      <td style={{ padding: "6px 12px", whiteSpace: "nowrap" }}>
        <div>
          <span style={{ color: "var(--text-primary)" }}>
            {dst.primary}
            {f.dstPort ? `:${f.dstPort}` : ""}
          </span>
          {dst.detail && (
            <span
              style={{
                marginLeft: "4px",
                fontSize: "11px",
                color: "var(--text-muted)",
              }}
            >
              {dst.detail}
            </span>
          )}
        </div>
      </td>
      <td style={{ padding: "6px 12px", color: "var(--text-muted)" }}>
        {f.protocol}
      </td>
      <td style={{ padding: "6px 12px" }}>
        <span
          style={{
            display: "inline-flex",
            alignItems: "center",
            padding: "2px 8px",
            borderRadius: "6px",
            fontSize: "11px",
            fontWeight: 600,
            ...verdictStyle(f.verdict),
          }}
          title={f.dropReason || undefined}
        >
          {f.verdict}
        </span>
        {f.dropReason && (
          <span
            style={{
              marginLeft: "4px",
              fontSize: "11px",
              color: "var(--error)",
            }}
          >
            {f.dropReason}
          </span>
        )}
      </td>
    </tr>
  );
}

function formatTime(iso: string): string {
  try {
    const d = new Date(iso);
    return d.toLocaleTimeString(undefined, {
      hour12: false,
      hour: "2-digit",
      minute: "2-digit",
      second: "2-digit",
      fractionalSecondDigits: 3,
    });
  } catch {
    return iso;
  }
}
