import { App, csp, staticFiles } from "fresh";
import type { State } from "@/utils.ts";

export const app = new App<State>();

// CSP via Fresh's built-in middleware — replaces manual headers in _middleware.ts.
// NOTE: 'unsafe-inline' remains in script-src because Fresh 2.x does not support
// per-request nonce injection into <script> tags. True nonce-based CSP is deferred
// until Fresh adds native nonce support.
app.use(csp({
  csp: [
    "default-src 'self'",
    "script-src 'self' 'unsafe-inline' https://esm.sh/",
    "style-src 'self' 'unsafe-inline' https://esm.sh/",
    "img-src 'self' data:",
    "connect-src 'self' https://esm.sh/",
    "worker-src 'self' blob: https://esm.sh/",
    "frame-src 'self'",
    "frame-ancestors 'none'",
    "base-uri 'self'",
    "object-src 'none'",
    "form-action 'self'",
  ],
}));

// Non-CSP security headers (no Fresh built-in for these)
app.use(async (ctx) => {
  const res = await ctx.next();
  res.headers.set("X-Frame-Options", "DENY");
  res.headers.set("X-Content-Type-Options", "nosniff");
  res.headers.set("Referrer-Policy", "strict-origin-when-cross-origin");
  res.headers.set(
    "Permissions-Policy",
    "camera=(), microphone=(), geolocation=()",
  );
  return res;
});

app.use(staticFiles());

app.fsRoutes();
