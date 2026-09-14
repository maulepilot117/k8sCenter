import type { IncomingMessage, ServerResponse } from "node:http";
import { applySecurityHeaders } from "./headers.ts";
import { handleWsHttpRequest } from "./ws-proxy.ts";

/**
 * The outermost request dispatch (KTD3): security headers first, then a
 * cheap malformed-request guard, then the /ws/* 426 branch, then static
 * assets, and finally Astro as the fallthrough. See prod.ts for the
 * concrete wiring (createServer + listen); this module exists so the
 * composition itself is testable without a real Astro build.
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
}

/**
 * Composes the full non-upgrade dispatch chain used by the built server
 * (prod.ts). Order matters and is the point of this function: headers ->
 * malformed guard -> /ws 426 -> static -> Astro.
 */
export function createRequestListener(
  options: RequestListenerOptions,
): (req: IncomingMessage, res: ServerResponse) => void {
  return (req, res) => {
    applySecurityHeaders(res);

    if (rejectMalformed(req, res)) return;
    if (handleWsHttpRequest(req, res)) return;
    if (options.serveStatic(req, res)) return;

    void options.astroHandler(req, res);
  };
}
