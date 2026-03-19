import { App, staticFiles } from "fresh";
import type { State } from "@/utils.ts";

export const app = new App<State>();

// Security headers — set CSP manually instead of Fresh's csp() middleware.
// Fresh's csp() prepends a hardcoded default CSP with `upgrade-insecure-requests`
// which breaks HTTP-only deployments (homelab, dev) by forcing the browser to
// load all subresources over HTTPS. Setting the header ourselves avoids this.
app.use(async (ctx) => {
  const res = await ctx.next();
  res.headers.set(
    "Content-Security-Policy",
    [
      "default-src 'self'",
      "script-src 'self' 'unsafe-inline' https://esm.sh/",
      "style-src 'self' 'unsafe-inline' https://esm.sh/",
      "font-src 'self'",
      "img-src 'self' data:",
      "connect-src 'self' https://esm.sh/",
      "worker-src 'self' blob: https://esm.sh/",
      "frame-src 'self'",
      "frame-ancestors 'none'",
      "base-uri 'self'",
      "object-src 'none'",
      "form-action 'self'",
    ].join("; "),
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

app.use(staticFiles());

app.fsRoutes();
