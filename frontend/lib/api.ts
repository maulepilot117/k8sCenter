/**
 * Client-only module — MUST NOT be imported in server-rendered components.
 * Module-level variables (accessToken, refreshPromise) are process-global
 * singletons in Deno; importing this server-side would leak auth state
 * across SSR requests.
 */
import { selectedCluster } from "@/lib/cluster.ts";
import type { APIError, APIResponse } from "@/lib/k8s-types.ts";
import type {
  AppNotification,
  NotifChannel,
  NotifChannelInput,
  NotifListParams,
  NotifRule,
  NotifRuleInput,
} from "@/lib/notif-center-types.ts";
import type {
  LimitsStatus,
  NamespaceLimits,
  NamespaceSummary,
} from "@/lib/limits-types.ts";

/** In-memory access token. Never stored in localStorage. */
let accessToken: string | null = null;

/** Track in-flight refresh to avoid concurrent refresh requests. */
let refreshPromise: Promise<boolean> | null = null;

/** Optional callback invoked on 403 responses — used by auth.ts to refresh permissions. */
let on403Callback: (() => void) | null = null;

/** Register a callback for 403 responses (permission denied). */
export function onForbidden(cb: () => void) {
  on403Callback = cb;
}

export function setAccessToken(token: string | null) {
  accessToken = token;
}

export function getAccessToken(): string | null {
  return accessToken;
}

/** Typed API error class. */
export class ApiError extends Error {
  constructor(
    public status: number,
    public code: number,
    public detail?: string,
  ) {
    super(`API error ${code}: ${detail ?? "Unknown error"}`);
    this.name = "ApiError";
  }
}

/**
 * Attempt to refresh the access token using the httpOnly refresh cookie.
 * Returns true if refresh succeeded.
 */
async function refreshAccessToken(): Promise<boolean> {
  try {
    const res = await fetch("/api/v1/auth/refresh", {
      method: "POST",
      credentials: "include",
      headers: {
        "X-Requested-With": "XMLHttpRequest",
      },
    });
    if (!res.ok) return false;

    const body = await res.json();
    if (body.data?.accessToken) {
      accessToken = body.data.accessToken;
      return true;
    }
    return false;
  } catch {
    return false;
  }
}

/**
 * Typed fetch wrapper for the k8sCenter API.
 *
 * - Injects Bearer token and X-Cluster-ID header
 * - Auto-refreshes on 401 (single concurrent refresh, replays queued requests)
 * - Parses error responses into ApiError
 */
export async function api<T>(
  path: string,
  options: RequestInit = {},
): Promise<APIResponse<T>> {
  const doFetch = (): Promise<Response> => {
    const headers = new Headers(options.headers);
    if (accessToken) {
      headers.set("Authorization", `Bearer ${accessToken}`);
    }
    headers.set("X-Cluster-ID", selectedCluster.value);
    if (!headers.has("Content-Type") && options.body) {
      headers.set("Content-Type", "application/json");
    }
    // CSRF protection for state-changing requests
    if (options.method && options.method !== "GET") {
      headers.set("X-Requested-With", "XMLHttpRequest");
    }

    return fetch(`/api${path}`, {
      ...options,
      headers,
      credentials: "include",
    });
  };

  let res = await doFetch();

  // On 401, attempt a single token refresh and retry.
  // Check even when accessToken is null — after a full page reload the
  // in-memory token is gone but the httpOnly refresh cookie may still exist.
  if (res.status === 401) {
    if (!refreshPromise) {
      refreshPromise = refreshAccessToken().finally(() => {
        refreshPromise = null;
      });
    }
    const refreshed = await refreshPromise;
    if (refreshed) {
      res = await doFetch();
    } else {
      // Refresh failed — clear token and redirect to login
      accessToken = null;
      if (typeof globalThis.document !== "undefined") {
        globalThis.location.href = "/login";
      }
      throw new ApiError(401, 401, "Session expired");
    }
  }

  if (!res.ok) {
    let errorBody: APIError | undefined;
    try {
      errorBody = await res.json();
    } catch {
      // Response wasn't JSON
    }
    // On 403, notify auth layer to refresh permissions (self-correcting mechanism)
    if (res.status === 403 && on403Callback) {
      on403Callback();
    }
    throw new ApiError(
      res.status,
      errorBody?.error?.code ?? res.status,
      errorBody?.error?.message ?? res.statusText,
    );
  }

  // 204 No Content has no body — return empty envelope instead of failing on res.json()
  if (res.status === 204) {
    return { data: undefined as unknown as T } as APIResponse<T>;
  }

  return await res.json();
}

/** Convenience methods. */
export const apiGet = <T>(path: string) => api<T>(path, { method: "GET" });

export const apiPost = <T>(path: string, body?: unknown) =>
  api<T>(path, {
    method: "POST",
    body: body ? JSON.stringify(body) : undefined,
  });

export const apiPut = <T>(path: string, body: unknown) =>
  api<T>(path, {
    method: "PUT",
    body: JSON.stringify(body),
  });

export async function apiDelete(path: string): Promise<void> {
  await api<unknown>(path, { method: "DELETE" });
}

/** POST with a raw string body (e.g., YAML content). */
export const apiPostRaw = <T>(
  path: string,
  body: string,
  contentType = "text/yaml",
) =>
  api<T>(path, {
    method: "POST",
    body,
    headers: { "Content-Type": contentType },
  });

// --- Notification Center API ---

function notifQueryString(params: NotifListParams): string {
  const p = new URLSearchParams();
  if (params.source) p.set("source", params.source);
  if (params.severity) p.set("severity", params.severity);
  if (params.read) p.set("read", params.read);
  if (params.since) p.set("since", params.since);
  if (params.until) p.set("until", params.until);
  if (params.limit !== undefined) p.set("limit", String(params.limit));
  if (params.offset !== undefined) p.set("offset", String(params.offset));
  const qs = p.toString();
  return qs ? `?${qs}` : "";
}

export const notifApi = {
  // Feed
  list: (params: NotifListParams = {}) =>
    apiGet<{ data: AppNotification[]; metadata: { total: number } }>(
      `/v1/notifications${notifQueryString(params)}`,
    ),
  unreadCount: () =>
    apiGet<{ data: { count: number } }>("/v1/notifications/unread-count"),
  markRead: (id: string) => apiPost<void>(`/v1/notifications/${id}/read`),
  /** Fire-and-forget markRead that survives page navigation. */
  markReadQuiet: (id: string) =>
    api<void>(`/v1/notifications/${id}/read`, {
      method: "POST",
      keepalive: true,
    }),
  markAllRead: () => apiPost<void>("/v1/notifications/read-all"),

  // Channels (admin)
  listChannels: () =>
    apiGet<{ data: NotifChannel[] }>("/v1/notifications/channels"),
  createChannel: (ch: NotifChannelInput) =>
    apiPost<{ data: { id: string } }>("/v1/notifications/channels", ch),
  updateChannel: (id: string, ch: NotifChannelInput) =>
    apiPut<void>(`/v1/notifications/channels/${id}`, ch),
  deleteChannel: (id: string) => apiDelete(`/v1/notifications/channels/${id}`),
  testChannel: (id: string) =>
    apiPost<{ data: { status: string } }>(
      `/v1/notifications/channels/${id}/test`,
    ),

  // Rules (admin)
  listRules: () => apiGet<{ data: NotifRule[] }>("/v1/notifications/rules"),
  createRule: (rule: NotifRuleInput) =>
    apiPost<{ data: { id: string } }>("/v1/notifications/rules", rule),
  updateRule: (id: string, rule: NotifRuleInput) =>
    apiPut<void>(`/v1/notifications/rules/${id}`, rule),
  deleteRule: (id: string) => apiDelete(`/v1/notifications/rules/${id}`),
};

// Namespace limits API (ResourceQuota + LimitRange management)
export const limitsApi = {
  status: () => apiGet<LimitsStatus>("/v1/limits/status"),
  list: () => apiGet<NamespaceSummary[]>("/v1/limits/namespaces"),
  get: (namespace: string) =>
    apiGet<NamespaceLimits>(`/v1/limits/namespaces/${namespace}`),
};
