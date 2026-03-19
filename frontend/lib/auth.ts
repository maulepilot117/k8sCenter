/**
 * Client-only module — MUST NOT be imported in server-rendered components.
 * Module-level signals are process-global singletons in Deno; importing
 * this server-side would leak auth state across SSR requests.
 */
import { computed, signal } from "@preact/signals";
import { api, getAccessToken, onForbidden, setAccessToken } from "@/lib/api.ts";
import type { RBACSummary, UserInfo } from "@/lib/k8s-types.ts";
import { selectedNamespace } from "@/lib/namespace.ts";

/** Reactive user state. */
const userSignal = signal<UserInfo | null>(null);
const loadingSignal = signal(false);

/** RBAC permissions from /auth/me — keyed by namespace + cluster-scoped. */
const rbacSignal = signal<RBACSummary | null>(null);

/** Whether the user is authenticated. */
const isAuthenticated = computed(() => userSignal.value !== null);

// Self-correcting: on any 403, refresh RBAC permissions for the current namespace.
// This handles the case where permissions changed while the user was active.
onForbidden(() => {
  const ns = selectedNamespace.value;
  if (ns && ns !== "all") {
    refreshPermissions(ns);
  }
});

/**
 * Log in with username and password.
 * Stores the access token in memory and fetches user info.
 * @param provider — optional provider ID (default: "local"). Use LDAP provider ID for LDAP login.
 */
export async function login(
  username: string,
  password: string,
  provider?: string,
): Promise<void> {
  const body: Record<string, string> = { username, password };
  if (provider && provider !== "local") {
    body.provider = provider;
  }

  const res = await api<{ accessToken: string; expiresIn: number }>(
    "/v1/auth/login",
    {
      method: "POST",
      body: JSON.stringify(body),
    },
  );
  setAccessToken(res.data.accessToken);
  // Backend login returns only the token — fetch user info separately
  await fetchCurrentUser();
}

/**
 * Handle the OIDC callback by exchanging the httpOnly cookie for an access token.
 * Called from the /auth/callback page after an OIDC redirect.
 */
export async function handleOIDCCallback(): Promise<boolean> {
  try {
    const res = await api<{ accessToken: string }>(
      "/auth/oidc-token-exchange",
      {
        method: "POST",
      },
    );
    if (res.data.accessToken) {
      setAccessToken(res.data.accessToken);
      await fetchCurrentUser();
      return true;
    }
    return false;
  } catch {
    return false;
  }
}

/**
 * Log out — invalidate refresh token, clear local state.
 */
export async function logout(): Promise<void> {
  try {
    await api("/v1/auth/logout", { method: "POST" });
  } catch {
    // Best-effort — clear local state regardless
  }
  setAccessToken(null);
  userSignal.value = null;
  rbacSignal.value = null;
}

/**
 * Fetch current user info and RBAC permissions from /auth/me.
 * Optionally scoped to a single namespace for efficiency.
 */
export async function fetchCurrentUser(
  namespace?: string,
): Promise<UserInfo | null> {
  if (!getAccessToken()) return null;
  try {
    loadingSignal.value = true;
    const params = namespace
      ? `?namespace=${encodeURIComponent(namespace)}`
      : "";
    const res = await api<{ user: UserInfo; rbac: RBACSummary }>(
      `/v1/auth/me${params}`,
      { method: "GET" },
    );
    userSignal.value = res.data.user;
    rbacSignal.value = res.data.rbac;
    return res.data.user;
  } catch {
    userSignal.value = null;
    rbacSignal.value = null;
    return null;
  } finally {
    loadingSignal.value = false;
  }
}

/**
 * Re-fetch RBAC permissions for a specific namespace.
 * Called when the namespace selector changes.
 */
export async function refreshPermissions(namespace: string): Promise<void> {
  if (!getAccessToken()) return;
  try {
    const res = await api<{ user: UserInfo; rbac: RBACSummary }>(
      `/v1/auth/me?namespace=${encodeURIComponent(namespace)}`,
      { method: "GET" },
    );
    rbacSignal.value = res.data.rbac;
  } catch {
    // Clear stale permissions from previous namespace — prevents showing
    // actions that were valid for namespace A but not namespace B.
    // canPerform defaults to true when null, so actions show optimistically.
    rbacSignal.value = null;
  }
}

/**
 * Reactive auth state for use in islands.
 */
export function useAuth() {
  return {
    user: userSignal,
    rbac: rbacSignal,
    isAuthenticated,
    loading: loadingSignal,
    login,
    logout,
    fetchCurrentUser,
    refreshPermissions,
  };
}
