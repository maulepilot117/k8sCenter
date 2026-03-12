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
 */
export async function login(
  username: string,
  password: string,
): Promise<void> {
  const res = await api<{ accessToken: string; user: UserInfo }>(
    "/v1/auth/login",
    {
      method: "POST",
      body: JSON.stringify({ username, password }),
      headers: {
        "Content-Type": "application/json",
        "X-Requested-With": "XMLHttpRequest",
      },
    },
  );
  setAccessToken(res.data.accessToken);
  userSignal.value = res.data.user;
}

/**
 * Log out — invalidate refresh token, clear local state.
 */
export async function logout(): Promise<void> {
  try {
    await api("/v1/auth/logout", {
      method: "POST",
      headers: { "X-Requested-With": "XMLHttpRequest" },
    });
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
    const res = await api<UserInfo>("/v1/auth/me", { method: "GET" });
    userSignal.value = res.data;
    return res.data;
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
