/**
 * Client-only module — MUST NOT be imported in server-rendered components.
 * Module-level signals are process-global singletons in Deno; importing
 * this server-side would leak auth state across SSR requests.
 */
import { computed, signal } from "@preact/signals";
import { api, getAccessToken, setAccessToken } from "@/lib/api.ts";
import type { UserInfo } from "@/lib/k8s-types.ts";

/** Reactive user state. */
const userSignal = signal<UserInfo | null>(null);
const loadingSignal = signal(false);

/** Whether the user is authenticated. */
const isAuthenticated = computed(() => userSignal.value !== null);

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
}

/**
 * Fetch current user info from /auth/me.
 * Called on app load to check if the session is still valid.
 */
export async function fetchCurrentUser(): Promise<UserInfo | null> {
  if (!getAccessToken()) return null;
  try {
    loadingSignal.value = true;
    const res = await api<{ user: UserInfo; rbac: unknown }>(
      "/v1/auth/me",
      { method: "GET" },
    );
    userSignal.value = res.data.user;
    return res.data.user;
  } catch {
    userSignal.value = null;
    return null;
  } finally {
    loadingSignal.value = false;
  }
}

/**
 * Reactive auth state for use in islands.
 */
export function useAuth() {
  return {
    user: userSignal,
    isAuthenticated,
    loading: loadingSignal,
    login,
    logout,
    fetchCurrentUser,
  };
}
