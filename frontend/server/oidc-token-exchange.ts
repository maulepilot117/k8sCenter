import type { IncomingMessage, ServerResponse } from "node:http";

/**
 * BFF endpoint for OIDC token exchange (U6), ported unchanged (R5) from
 * frontend/routes/api/auth/oidc-token-exchange.ts. That file's handler has
 * no page body -- it's a bare API route, not something the bulk `[...path]`
 * catch-all proxy could handle -- so it gets its own module here, wired
 * into dispatch.ts as an exact-path match ahead of the generic proxy.
 *
 * After the OIDC callback, the Go backend sets an httpOnly cookie
 * (`oidc_access_token`) containing the k8sCenter JWT access token. This
 * endpoint reads that cookie, returns the token in the response body, and
 * clears the cookie (single-use).
 *
 * This prevents the access token from being exposed in URL fragments or
 * query parameters during the OIDC redirect flow.
 *
 * KTD13: the cookie's name, encoding, and lifetime (single-use, cleared on
 * read) must stay byte-identical to the Fresh version -- a rollback
 * mid-login is the one flow that can otherwise strand an operator.
 */

/** The route this handler owns. Matched by dispatch.ts before the generic proxy. */
export const OIDC_TOKEN_EXCHANGE_PATH = "/api/auth/oidc-token-exchange";

const COOKIE_NAME = "oidc_access_token";
const CLEAR_COOKIE = `${COOKIE_NAME}=; Path=/api/auth/oidc-token-exchange; HttpOnly; SameSite=Lax; Max-Age=0`;

/** Simple cookie parser -- ported verbatim. */
export function parseCookies(header: string): Record<string, string> {
  const cookies: Record<string, string> = {};
  for (const pair of header.split(";")) {
    const [key, ...rest] = pair.trim().split("=");
    if (key) {
      cookies[key] = rest.join("=");
    }
  }
  return cookies;
}

function sendJson(
  res: ServerResponse,
  status: number,
  body: unknown,
  extraHeaders?: Record<string, string>,
): void {
  res.statusCode = status;
  res.setHeader("Content-Type", "application/json");
  if (extraHeaders) {
    for (const [name, value] of Object.entries(extraHeaders)) {
      res.setHeader(name, value);
    }
  }
  res.end(JSON.stringify(body));
}

/**
 * Handles a request to the token-exchange path. Only POST is supported, to
 * match the Fresh route (`define.handlers({ POST(ctx) { ... } })`, which
 * only defines a POST branch -- Fresh answers 405 for other methods on a
 * defined route; this mirrors that instead of silently accepting GET).
 */
export function handleOIDCTokenExchange(
  req: IncomingMessage,
  res: ServerResponse,
): void {
  if ((req.method ?? "GET").toUpperCase() !== "POST") {
    res.statusCode = 405;
    res.setHeader("Allow", "POST");
    res.end("Method Not Allowed");
    return;
  }

  // CSRF protection: require X-Requested-With header (cannot be sent
  // cross-origin without a CORS preflight).
  if (!req.headers["x-requested-with"]) {
    sendJson(res, 403, {
      error: { code: 403, message: "Missing X-Requested-With header" },
    });
    return;
  }

  const cookieHeader = req.headers.cookie ?? "";
  const cookies = parseCookies(cookieHeader);
  const accessToken = cookies[COOKIE_NAME];

  if (!accessToken) {
    sendJson(res, 401, {
      error: { code: 401, message: "No OIDC token available" },
    });
    return;
  }

  // Clear the cookie -- single-use.
  sendJson(res, 200, { data: { accessToken } }, { "Set-Cookie": CLEAR_COOKIE });
}
