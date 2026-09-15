/**
 * Client-only module — MUST NOT be imported in server-rendered components.
 * Module-level variables (accessToken, refreshPromise) are process-global
 * singletons in Deno; importing this server-side would leak auth state
 * across SSR requests.
 */
import { selectedCluster } from "@/lib/cluster.ts";
import type { APIError, APIResponse } from "@/lib/k8s-types.ts";
import type {
  LimitsStatus,
  NamespaceLimits,
  NamespaceSummary,
} from "@/lib/limits-types.ts";
import type {
  AppNotification,
  NotifChannel,
  NotifChannelInput,
  NotifListParams,
  NotifRule,
  NotifRuleInput,
} from "@/lib/notif-center-types.ts";

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
  /**
   * Raw error response body when present. Carries the full error envelope —
   * `error.code`, `error.message`, `error.reason`, `error.extra`. Consumers
   * should prefer the typed `reason` field on this class and the
   * `errorExtra(err, key)` helper instead of reading `body.error` directly.
   */
  body?: {
    error?: {
      code?: number;
      message?: string;
      detail?: string;
      reason?: string;
      extra?: Record<string, unknown>;
    };
  };

  /** Endpoint-specific reason code (e.g. "active_job_exists", "scope_changed"). */
  reason?: string;

  constructor(
    public status: number,
    public code: number,
    public detail?: string,
    body?: {
      error?: {
        code?: number;
        message?: string;
        detail?: string;
        reason?: string;
        extra?: Record<string, unknown>;
      };
    },
  ) {
    super(`API error ${code}: ${detail ?? "Unknown error"}`);
    this.name = "ApiError";
    this.body = body;
    this.reason = body?.error?.reason;
  }
}

/**
 * Typed accessor for endpoint-specific error extras carried in `error.extra`.
 * Returns the stringified value or undefined. Avoids `as string` casts at
 * call sites. Pair with ApiError.reason for the full canonical envelope.
 */
export function errorExtra(err: ApiError, key: string): string | undefined {
  const v = err.body?.error?.extra?.[key];
  return typeof v === "string" ? v : undefined;
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

    const body = (await res.json()) as APIResponse<{ accessToken: string }>;
    if (body.data?.accessToken) {
      accessToken = body.data.accessToken;
      return true;
    }
    return false;
  } catch (e) {
    console.info("token refresh failed:", e);
    return false;
  }
}

/**
 * Per-request transport controls shared by `api()` and every convenience
 * wrapper below.
 */
export interface RequestTargeting {
  /**
   * Cooperative cancellation. Pass `AbortController.signal` to cancel an
   * in-flight request — for example when the cluster or route changes while a
   * response is still outstanding.
   */
  signal?: AbortSignal;
  /**
   * Cluster this request is addressed to, overriding the ambient
   * `selectedCluster` signal for this call only.
   *
   * Pass it whenever the target must not be whatever the operator happens to
   * be looking at when the request is issued: a YAML apply pinned to the
   * cluster whose preview was reviewed (D4), or a capability read whose URL
   * path already names a cluster and whose header must agree with it or the
   * handler 409s.
   */
  clusterId?: string;
}

/**
 * Normalizes the optional last argument of the convenience wrappers, which
 * accept either a bare AbortSignal (the original signature, still used by
 * existing call sites) or the fuller targeting object.
 */
function targeting(opts?: AbortSignal | RequestTargeting): RequestTargeting {
  if (!opts) return {};
  return opts instanceof AbortSignal ? { signal: opts } : opts;
}

/**
 * Typed fetch wrapper for the k8sCenter API.
 *
 * - Injects Bearer token and X-Cluster-ID header
 * - Auto-refreshes on 401 (single concurrent refresh, replays queued requests)
 * - Parses error responses into ApiError
 * - Accepts an optional `signal` for cooperative cancellation; pass
 *   `AbortController.signal` from the caller to cancel in-flight requests.
 *
 * The target cluster is decided **once, here**, and every attempt this call
 * makes carries that same decision. It used to be read from
 * `selectedCluster.value` inside `doFetch`, which is invoked a second time on
 * the 401-refresh path below: a cluster switch landing during a token refresh
 * silently retargeted the retry, so a request the operator issued against one
 * cluster completed against another (AE2). Capturing it outside the closure is
 * the whole fix — do not move this read back in.
 */
export async function api<T>(
  path: string,
  options: RequestInit & RequestTargeting = {},
): Promise<APIResponse<T>> {
  const { clusterId, ...init } = options;
  const targetCluster = clusterId ?? selectedCluster.value;

  const doFetch = (): Promise<Response> => {
    const headers = new Headers(init.headers);
    if (accessToken) {
      headers.set("Authorization", `Bearer ${accessToken}`);
    }
    headers.set("X-Cluster-ID", targetCluster);
    if (!headers.has("Content-Type") && init.body) {
      headers.set("Content-Type", "application/json");
    }
    // CSRF protection for state-changing requests
    if (init.method && init.method !== "GET") {
      headers.set("X-Requested-With", "XMLHttpRequest");
    }

    return fetch(`/api${path}`, {
      ...init,
      headers,
      credentials: "include",
      signal: init.signal,
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
    // On 403, notify auth layer to refresh permissions (self-correcting mechanism).
    //
    // Never for /v1/auth/* — the callback's own endpoint cannot be repaired by
    // re-calling it. `refreshPermissions` re-issues GET /v1/auth/me, which is
    // inside the backend's ClusterContext group and therefore 403s for a
    // non-admin carrying a non-local X-Cluster-ID. Without this guard that is
    // a self-feeding loop: one unthrottled request per round-trip, forever,
    // against an endpoint with no rate limit.
    if (res.status === 403 && on403Callback && !path.startsWith("/v1/auth/")) {
      on403Callback();
    }
    throw new ApiError(
      res.status,
      errorBody?.error?.code ?? res.status,
      errorBody?.error?.message ?? res.statusText,
      errorBody as { error?: Record<string, unknown> } | undefined,
    );
  }

  // 204 No Content has no body — return empty envelope instead of failing on res.json()
  if (res.status === 204) {
    return { data: undefined as unknown as T } as APIResponse<T>;
  }

  return await res.json();
}

/**
 * Convenience methods.
 *
 * The trailing options argument accepts either a bare AbortSignal or a
 * `RequestTargeting` object. The bare form is the pre-existing signature and
 * stays supported so the two call sites that use it keep working unchanged;
 * new call sites should pass the object, which can also pin `clusterId`.
 */
export const apiGet = <T>(
  path: string,
  opts?: AbortSignal | RequestTargeting,
) => api<T>(path, { method: "GET", ...targeting(opts) });

export const apiPost = <T>(
  path: string,
  body?: unknown,
  opts?: AbortSignal | RequestTargeting,
) =>
  api<T>(path, {
    method: "POST",
    body: body ? JSON.stringify(body) : undefined,
    ...targeting(opts),
  });

export const apiPut = <T>(
  path: string,
  body: unknown,
  opts?: AbortSignal | RequestTargeting,
) =>
  api<T>(path, {
    method: "PUT",
    body: JSON.stringify(body),
    ...targeting(opts),
  });

export async function apiDelete(
  path: string,
  opts?: AbortSignal | RequestTargeting,
): Promise<void> {
  await api<unknown>(path, { method: "DELETE", ...targeting(opts) });
}

/**
 * POST with a raw string body (e.g., YAML content).
 *
 * Cancellable and pinnable: the YAML flow issues a preview and an apply
 * against a target the operator reviewed, and must be able to both abandon an
 * in-flight preview when the operator switches clusters and address the apply
 * to the reviewed cluster rather than the current one.
 */
export const apiPostRaw = <T>(
  path: string,
  body: string,
  contentType = "text/yaml",
  opts?: AbortSignal | RequestTargeting,
) =>
  api<T>(path, {
    method: "POST",
    body,
    headers: { "Content-Type": contentType },
    ...targeting(opts),
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
