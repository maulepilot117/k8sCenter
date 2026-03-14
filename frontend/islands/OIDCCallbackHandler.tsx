import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { useEffect } from "preact/hooks";
import { handleOIDCCallback } from "@/lib/auth.ts";

/**
 * Handles the post-OIDC-callback token exchange.
 * Reads the access token from the httpOnly cookie via a BFF endpoint,
 * stores it in memory, and redirects to the dashboard.
 */
export default function OIDCCallbackHandler() {
  const error = useSignal("");
  const processing = useSignal(true);

  useEffect(() => {
    if (!IS_BROWSER) return;

    handleOIDCCallback().then((success) => {
      if (success) {
        globalThis.location.href = "/";
      } else {
        error.value = "Authentication failed. Please try again.";
        processing.value = false;
      }
    });
  }, []);

  if (error.value) {
    return (
      <div>
        <div class="rounded-md bg-red-50 px-4 py-3 text-sm text-red-800 dark:bg-red-900/30 dark:text-red-400">
          {error.value}
        </div>
        <a
          href="/login"
          class="mt-4 inline-block text-sm text-blue-600 hover:text-blue-500 dark:text-blue-400"
        >
          Back to login
        </a>
      </div>
    );
  }

  return (
    <div class="space-y-4">
      <div class="mx-auto h-8 w-8 animate-spin rounded-full border-2 border-slate-300 border-t-blue-600" />
      <p class="text-sm text-slate-500 dark:text-slate-400">
        Completing sign in...
      </p>
    </div>
  );
}
