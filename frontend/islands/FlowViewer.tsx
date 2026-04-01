import { useSignal } from "@preact/signals";
import { useEffect, useRef } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiGet, getAccessToken } from "@/lib/api.ts";
import { useNamespaces } from "@/lib/hooks/use-namespaces.ts";
import { selectedNamespace } from "@/lib/namespace.ts";
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

function verdictBadgeClass(verdict: string): string {
  switch (verdict) {
    case "FORWARDED":
      return "bg-success-dim text-success";
    case "DROPPED":
      return "bg-danger-dim text-danger";
    case "AUDIT":
      return "bg-warning-dim text-warning";
    case "ERROR":
      return "bg-danger-dim text-danger";
    default:
      return "bg-elevated text-text-secondary bg-surface text-text-secondary";
  }
}

export default function FlowViewer() {
  const namespace = useSignal(
    IS_BROWSER && selectedNamespace.value !== "all"
      ? selectedNamespace.value
      : "default",
  );
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
      <div class="p-6">
        <h1 class="text-2xl font-semibold text-text-primary">
          Network Flows
        </h1>
      </div>
    );
  }

  return (
    <div class="p-6">
      <div class="flex items-center justify-between mb-6">
        <div class="flex items-center gap-3">
          <h1 class="text-2xl font-semibold text-text-primary">
            Network Flows
          </h1>
          {wsState.value === "live" && (
            <span class="inline-flex items-center gap-1.5 rounded-full px-2.5 py-0.5 text-xs font-medium bg-success-dim text-success">
              <span class="h-1.5 w-1.5 rounded-full bg-success animate-pulse" />
              Live
            </span>
          )}
          {wsState.value === "connecting" && (
            <span class="inline-flex items-center gap-1.5 rounded-full px-2.5 py-0.5 text-xs font-medium bg-warning-dim text-warning">
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
      <div class="flex items-center gap-4 mb-4">
        <div>
          <label class="block text-xs font-medium text-text-muted mb-1">
            Namespace
          </label>
          <select
            value={namespace.value}
            onChange={(e) =>
              namespace.value = (e.target as HTMLSelectElement).value}
            class="rounded-md border border-border-primary bg-surface px-3 py-1.5 text-sm text-text-primary"
          >
            {namespaces.value.map((ns) => (
              <option key={ns} value={ns}>{ns}</option>
            ))}
          </select>
        </div>
        <div>
          <label class="block text-xs font-medium text-text-muted mb-1">
            Verdict
          </label>
          <select
            value={verdict.value}
            onChange={(e) =>
              verdict.value = (e.target as HTMLSelectElement).value}
            class="rounded-md border border-border-primary bg-surface px-3 py-1.5 text-sm text-text-primary"
          >
            {VERDICT_OPTIONS.map((v) => (
              <option key={v} value={v}>{v || "All"}</option>
            ))}
          </select>
        </div>
        <div class="text-xs text-text-muted self-end pb-1.5">
          {flows.value.length} flows
        </div>
      </div>

      {error.value && (
        <div class="mb-4 rounded-md bg-danger-dim p-3 border border-danger">
          <p class="text-sm text-danger">{error.value}</p>
        </div>
      )}

      {/* Flow table */}
      <div class="overflow-x-auto rounded-lg border border-border-primary">
        <table class="min-w-full text-sm">
          <thead>
            <tr class="bg-surface/50 text-left text-text-secondary">
              <th class="py-2 px-3 font-medium">Time</th>
              <th class="py-2 px-3 font-medium">Direction</th>
              <th class="py-2 px-3 font-medium">Source</th>
              <th class="py-2 px-3 font-medium">Destination</th>
              <th class="py-2 px-3 font-medium">Protocol</th>
              <th class="py-2 px-3 font-medium">Verdict</th>
            </tr>
          </thead>
          <tbody>
            {flows.value.length === 0 && !loading.value && (
              <tr>
                <td
                  colSpan={6}
                  class="py-8 px-3 text-center text-text-muted"
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
              <tr
                key={`${f.time}-${i}`}
                class="border-t border-border-subtle/50 hover:bg-hover/30"
              >
                <td class="py-1.5 px-3 text-text-muted whitespace-nowrap font-mono text-xs">
                  {formatTime(f.time)}
                </td>
                <td class="py-1.5 px-3">
                  <span class="text-xs text-text-secondary">
                    {f.direction === "INGRESS" ? "\u2B07" : "\u2B06"}
                    {""}
                    {f.direction}
                  </span>
                </td>
                <td class="py-1.5 px-3 whitespace-nowrap">
                  {(() => {
                    const ep = formatEndpoint(
                      f.srcNamespace,
                      f.srcPod,
                      f.srcIP,
                      f.srcNames,
                      f.srcService,
                      f.srcLabels,
                    );
                    return (
                      <div>
                        <span class="text-text-primary">
                          {ep.primary}
                        </span>
                        {ep.detail && (
                          <span class="ml-1 text-xs text-text-muted">
                            {ep.detail}
                          </span>
                        )}
                      </div>
                    );
                  })()}
                </td>
                <td class="py-1.5 px-3 whitespace-nowrap">
                  {(() => {
                    const ep = formatEndpoint(
                      f.dstNamespace,
                      f.dstPod,
                      f.dstIP,
                      f.dstNames,
                      f.dstService,
                      f.dstLabels,
                    );
                    return (
                      <div>
                        <span class="text-text-primary">
                          {ep.primary}
                          {f.dstPort ? `:${f.dstPort}` : ""}
                        </span>
                        {ep.detail && (
                          <span class="ml-1 text-xs text-text-muted">
                            {ep.detail}
                          </span>
                        )}
                      </div>
                    );
                  })()}
                </td>
                <td class="py-1.5 px-3 text-text-secondary">
                  {f.protocol}
                </td>
                <td class="py-1.5 px-3">
                  <span
                    class={`inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium ${
                      verdictBadgeClass(f.verdict)
                    }`}
                    title={f.dropReason || undefined}
                  >
                    {f.verdict}
                  </span>
                  {f.dropReason && (
                    <span class="ml-1 text-xs text-error">
                      {f.dropReason}
                    </span>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
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
