import { useSignal } from"@preact/signals";
import { IS_BROWSER } from"fresh/runtime";
import { useEffect } from"preact/hooks";
import { handleOIDCCallback } from"@/lib/auth.ts";

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
 globalThis.location.href ="/";
 } else {
 error.value ="Authentication failed. Please try again.";
 processing.value = false;
 }
 });
 }, []);

 if (error.value) {
 return (
 <div>
 <div class="rounded-md bg-danger-dim px-4 py-3 text-sm text-danger">
 {error.value}
 </div>
 <a
 href="/login"
 class="mt-4 inline-block text-sm text-blue-600 hover:text-blue-500 text-accent"
 >
 Back to login
 </a>
 </div>
 );
 }

 return (
 <div class="space-y-4">
 <div class="mx-auto h-8 w-8 animate-spin rounded-full border-2 border-border-primary border-t-blue-600" />
 <p class="text-sm text-text-muted">
 Completing sign in...
 </p>
 </div>
 );
}
