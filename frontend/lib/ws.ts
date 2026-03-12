import { signal } from "@preact/signals";
import { getAccessToken } from "@/lib/api.ts";
import { WS_RECONNECT_MAX_MS, WS_RECONNECT_MIN_MS } from "@/lib/constants.ts";

export type WsStatus =
  | "connecting"
  | "connected"
  | "reconnecting"
  | "disconnected";

export interface ResourceEvent {
  type: "ADDED" | "MODIFIED" | "DELETED";
  object: unknown;
}

export interface Subscription {
  kind: string;
  namespace?: string;
}

/** Reactive WebSocket connection status. */
export const wsStatus = signal<WsStatus>("disconnected");

let ws: WebSocket | null = null;
let reconnectDelay = WS_RECONNECT_MIN_MS;
let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
let subscriptions: Subscription[] = [];
const eventListeners: Map<string, Set<(event: ResourceEvent) => void>> =
  new Map();

function subscriptionKey(sub: Subscription): string {
  return `${sub.kind}:${sub.namespace ?? "*"}`;
}

/**
 * Connect to the WebSocket endpoint.
 * Authenticates with JWT as first message.
 */
export function connect() {
  if (
    ws?.readyState === WebSocket.OPEN || ws?.readyState === WebSocket.CONNECTING
  ) {
    return;
  }

  const token = getAccessToken();
  if (!token) return;

  const protocol = globalThis.location.protocol === "https:" ? "wss:" : "ws:";
  const url = `${protocol}//${globalThis.location.host}/api/v1/ws/resources`;

  wsStatus.value = subscriptions.length > 0 ? "reconnecting" : "connecting";
  ws = new WebSocket(url);

  ws.onopen = () => {
    // Authenticate
    ws!.send(JSON.stringify({ type: "auth", token }));
    wsStatus.value = "connected";
    reconnectDelay = WS_RECONNECT_MIN_MS;

    // Re-subscribe to previously active subscriptions
    for (const sub of subscriptions) {
      ws!.send(JSON.stringify({ subscribe: sub }));
    }
  };

  ws.onmessage = (event) => {
    try {
      const data = JSON.parse(event.data);
      if (
        data.type === "ADDED" || data.type === "MODIFIED" ||
        data.type === "DELETED"
      ) {
        const key = subscriptionKey({
          kind: data.object?.kind?.toLowerCase() ?? "",
          namespace: data.object?.metadata?.namespace,
        });
        const wildcardKey = subscriptionKey({
          kind: data.object?.kind?.toLowerCase() ?? "",
        });
        const listeners = eventListeners.get(key) ??
          eventListeners.get(wildcardKey);
        if (listeners) {
          for (const listener of listeners) {
            listener(data as ResourceEvent);
          }
        }
      }
    } catch {
      // Ignore malformed messages
    }
  };

  ws.onclose = () => {
    wsStatus.value = "disconnected";
    scheduleReconnect();
  };

  ws.onerror = () => {
    ws?.close();
  };
}

function scheduleReconnect() {
  if (reconnectTimer) return;
  reconnectTimer = setTimeout(() => {
    reconnectTimer = null;
    reconnectDelay = Math.min(reconnectDelay * 2, WS_RECONNECT_MAX_MS);
    connect();
  }, reconnectDelay);
}

/**
 * Subscribe to resource events.
 * Returns an unsubscribe function.
 */
export function subscribe(
  sub: Subscription,
  callback: (event: ResourceEvent) => void,
): () => void {
  const key = subscriptionKey(sub);

  // Track subscription
  if (!subscriptions.some((s) => subscriptionKey(s) === key)) {
    subscriptions.push(sub);
  }

  // Register listener
  if (!eventListeners.has(key)) {
    eventListeners.set(key, new Set());
  }
  eventListeners.get(key)!.add(callback);

  // Send subscribe message if connected
  if (ws?.readyState === WebSocket.OPEN) {
    ws.send(JSON.stringify({ subscribe: sub }));
  } else {
    connect();
  }

  return () => {
    const listeners = eventListeners.get(key);
    if (listeners) {
      listeners.delete(callback);
      if (listeners.size === 0) {
        eventListeners.delete(key);
        subscriptions = subscriptions.filter((s) => subscriptionKey(s) !== key);
        if (ws?.readyState === WebSocket.OPEN) {
          ws.send(JSON.stringify({ unsubscribe: sub }));
        }
      }
    }
  };
}

/** Disconnect and clean up. */
export function disconnect() {
  if (reconnectTimer) {
    clearTimeout(reconnectTimer);
    reconnectTimer = null;
  }
  ws?.close();
  ws = null;
  wsStatus.value = "disconnected";
  subscriptions = [];
  eventListeners.clear();
}
