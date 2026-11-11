import { useEffect, useState } from "react";
import { Spinner } from "@/components/ui/Spinner.tsx";
import { handleOIDCCallback } from "@/lib/auth.ts";
import { IS_BROWSER } from "@/src/lib/is-browser.ts";

/**
 * Handles the post-OIDC-callback token exchange.
 * Reads the access token from the httpOnly cookie via a BFF endpoint,
 * stores it in memory, and redirects to the dashboard.
 */
export default function OIDCCallbackHandler() {
  const [error, setError] = useState("");

  useEffect(() => {
    if (!IS_BROWSER) return;

    handleOIDCCallback().then((success) => {
      if (success) {
        globalThis.location.href = "/";
      } else {
        setError("Authentication failed. Please try again.");
      }
    });
  }, []);

  if (error) {
    return (
      <div>
        <div className="rounded-md bg-danger-dim px-4 py-3 text-sm text-danger">
          {error}
        </div>
        <a
          href="/login"
          className="mt-4 inline-block text-sm text-accent hover:text-accent"
        >
          Back to login
        </a>
      </div>
    );
  }

  return (
    <div className="space-y-4">
      <Spinner className="mx-auto text-accent" />
      <p className="text-sm text-text-muted">Completing sign in...</p>
    </div>
  );
}
