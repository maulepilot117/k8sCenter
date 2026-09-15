import type { ServerResponse } from "node:http";

/**
 * Security headers — set CSP manually instead of a framework's built-in csp()
 * middleware.
 *
 * Ported verbatim from frontend/main.ts (KTD3). Fresh's csp() middleware
 * prepends a hardcoded default CSP with `upgrade-insecure-requests`, which
 * breaks HTTP-only deployments (homelab, dev) by forcing the browser to load
 * all subresources over HTTPS. Setting the header ourselves avoids this. The
 * same reasoning is why Astro's own `security.csp` feature stays off: see
 * KTD3 in the migration plan — it emits only a <meta> tag for on-demand
 * pages, real headers only for prerendered ones, nothing at all in dev, and
 * meta-CSP cannot carry `frame-ancestors`.
 *
 * This is the single source of truth for the five headers. Both the built
 * server (prod.ts, via dispatch.ts) and the dev server (dev-plugin.ts) apply
 * them from here so the two never drift (R12, R18).
 */
export const SECURITY_HEADERS: ReadonlyArray<readonly [string, string]> = [
  [
    "Content-Security-Policy",
    [
      "default-src 'self'",
      "script-src 'self' 'unsafe-inline' https://esm.sh/",
      "style-src 'self' 'unsafe-inline' https://esm.sh/ https://fonts.googleapis.com/",
      "font-src 'self' https://fonts.gstatic.com/",
      "img-src 'self' data:",
      "connect-src 'self' https://esm.sh/",
      "worker-src 'self' blob: https://esm.sh/",
      "frame-src 'self'",
      "frame-ancestors 'none'",
      "base-uri 'self'",
      "object-src 'none'",
      "form-action 'self'",
    ].join("; "),
  ],
  ["X-Frame-Options", "DENY"],
  ["X-Content-Type-Options", "nosniff"],
  ["Referrer-Policy", "strict-origin-when-cross-origin"],
  ["Permissions-Policy", "camera=(), microphone=(), geolocation=()"],
];

/**
 * Sets all five security headers on a live node:http ServerResponse. Called
 * at the very start of the outer dispatch (KTD3) so every later branch —
 * static files, the 426/400/404 WS rejections that go through `res`, Astro's
 * own handler, and its 404/500 fallbacks — inherits them without having to
 * remember to do so itself.
 */
export function applySecurityHeaders(res: ServerResponse): void {
  for (const [name, value] of SECURITY_HEADERS) {
    res.setHeader(name, value);
  }
}

/**
 * Renders the header set as raw "Name: value" CRLF-joined lines, for the one
 * case that never gets a ServerResponse object to call setHeader on: a
 * rejected WebSocket upgrade, which is answered directly on the raw
 * net.Socket handed to the http server's 'upgrade' event (see ws-proxy.ts).
 */
export function rawHeaderLines(): string[] {
  return SECURITY_HEADERS.map(([name, value]) => `${name}: ${value}`);
}
