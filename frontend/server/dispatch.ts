import type { IncomingMessage, ServerResponse } from "node:http";
import {
  type ApiProxyOptions,
  handleApiProxy,
  isApiProxyPath,
} from "./api-proxy.ts";
import { applySecurityHeaders } from "./headers.ts";
import {
  handleOIDCTokenExchange,
  OIDC_TOKEN_EXCHANGE_PATH,
} from "./oidc-token-exchange.ts";
import { handleWsHttpRequest } from "./ws-proxy.ts";

/**
 * The outermost request dispatch (KTD3): security headers first, then a
 * cheap malformed-request guard, then the /ws/* 426 branch, then the BFF
 * API proxy (U6), then static assets, and finally Astro as the
 * fallthrough. See prod.ts for the concrete wiring (createServer +
 * listen); this module exists so the composition itself is testable
 * without a real Astro build.
 */

const ALLOWED_METHODS = new Set([
  "GET",
  "HEAD",
  "POST",
  "PUT",
  "PATCH",
  "DELETE",
  "OPTIONS",
]);
const MAX_URL_LENGTH = 8192;

/**
 * Rejects a request before any downstream branch runs: an unsupported
 * method, or a URL long enough to be a probe rather than a real navigation.
 * Returns true if it rejected (and answered) the request.
 */
export function rejectMalformed(
  req: IncomingMessage,
  res: ServerResponse,
): boolean {
  if (!req.method || !ALLOWED_METHODS.has(req.method)) {
    res.statusCode = 405;
    res.end("Method Not Allowed");
    return true;
  }
  if ((req.url ?? "").length > MAX_URL_LENGTH) {
    res.statusCode = 414;
    res.end("URI Too Long");
    return true;
  }
  return false;
}

export interface RequestListenerOptions {
  /** The compiled/dev Astro middleware handler: (req, res) -> void|Promise<void>. */
  astroHandler: (
    req: IncomingMessage,
    res: ServerResponse,
  ) => void | Promise<void>;
  /** Static asset handler from static.ts's createStaticHandler(clientDir). */
  serveStatic: (req: IncomingMessage, res: ServerResponse) => boolean;
  /** Overridable for tests; forwarded to handleApiProxy. */
  apiProxyOptions?: ApiProxyOptions;
}

/**
 * Routes a request under /api/: the exact-match token-exchange endpoint
 * first (it has no page body, so it can't go through the catch-all
 * proxy), then the generic v1/ proxy for everything else under /api/.
 * Both handlers fully answer the request themselves -- including their
 * own 400/401/403/502/504 -- so this always returns true for any /api/
 * path (matching the Fresh routes it replaces, which likewise always
 * produced a response rather than falling through to a 404 page).
 * Returns false for anything outside /api/ so dispatch continues to
 * static/Astro.
 */
export function dispatchApi(
  req: IncomingMessage,
  res: ServerResponse,
  apiProxyOptions?: ApiProxyOptions,
): boolean {
  const pathname = (req.url ?? "/").split("?")[0] ?? "/";

  if (pathname === OIDC_TOKEN_EXCHANGE_PATH) {
    handleOIDCTokenExchange(req, res);
    return true;
  }
  if (isApiProxyPath(pathname)) {
    void handleApiProxy(req, res, apiProxyOptions);
    return true;
  }
  return false;
}

/**
 * Composes the full non-upgrade dispatch chain used by the built server
 * (prod.ts). Order matters and is the point of this function: headers ->
 * malformed guard -> /ws 426 -> API proxy -> static -> Astro.
 */
export function createRequestListener(
  options: RequestListenerOptions,
): (req: IncomingMessage, res: ServerResponse) => void {
  return (req, res) => {
    applySecurityHeaders(res);

    if (rejectMalformed(req, res)) return;
    if (handleWsHttpRequest(req, res)) return;
    if (dispatchApi(req, res, options.apiProxyOptions)) return;
    if (options.serveStatic(req, res)) return;

    void options.astroHandler(req, res);
  };
}
