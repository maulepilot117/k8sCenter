import { define } from "@/utils.ts";

/**
 * Global middleware — sets default state values and security headers.
 * Client-side auth check happens in islands (not here, since
 * the JWT lives in browser memory, not in cookies accessible to SSR).
 */
export default define.middleware(async (ctx) => {
  ctx.state.user = null;
  ctx.state.title = "KubeCenter";

  const res = await ctx.next();

  // Security headers
  res.headers.set(
    "Content-Security-Policy",
    // TODO: replace 'unsafe-inline' on script-src with nonce-based CSP when Fresh supports it
    // esm.sh is allowed for Monaco editor CDN loading (Step 7), pinned to monaco-editor path
    "default-src 'self'; script-src 'self' 'unsafe-inline' https://esm.sh/monaco-editor@0.52.2/; style-src 'self' 'unsafe-inline' https://esm.sh/monaco-editor@0.52.2/; img-src 'self' data:; connect-src 'self' https://esm.sh/monaco-editor@0.52.2/; worker-src 'self' blob: https://esm.sh/monaco-editor@0.52.2/; frame-ancestors 'none'",
  );
  res.headers.set("X-Frame-Options", "DENY");
  res.headers.set("X-Content-Type-Options", "nosniff");
  res.headers.set("Referrer-Policy", "strict-origin-when-cross-origin");
  res.headers.set(
    "Permissions-Policy",
    "camera=(), microphone=(), geolocation=()",
  );

  return res;
});
