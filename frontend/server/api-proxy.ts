import type { IncomingMessage, ServerResponse } from "node:http";
import { Readable } from "node:stream";
import { BACKEND_URL } from "@/lib/constants.ts";

/**
 * The catch-all BFF proxy to the Go backend (U6), ported unchanged (R5)
 * from frontend/routes/api/[...path].ts. That file stays live until U12 --
 * this is the server-side twin the built Bun server actually runs.
 *
 * Ported unchanged:
 *  - the forward-header allowlist
 *  - the hop-by-hop response-header strip list
 *  - the `v1/` prefix requirement + traversal regex (SSRF guard)
 *  - the 30s timeout and the 504-timeout / 502-other-failure split
 *  - manual handling of the two OIDC redirect paths (redirect: "manual")
 *
 * Dropped deliberately (see U6 in the migration plan): the
 * `duplex: "half"` cast Deno's fetch needed for a streamed request body.
 * A spike measured a streamed body reaching a backend stub identically
 * with and without it under Bun 1.4.2 -- it is Node-fetch-specific dead
 * code here, not a portability hedge.
 */

/** Headers to forward from client to backend. */
export const FORWARD_HEADERS = [
  "authorization",
  "content-type",
  "accept",
  "x-requested-with",
  "x-cluster-id",
  "cookie",
];

/** Hop-by-hop headers to strip from backend responses. */
export const HOP_BY_HOP_HEADERS = [
  "connection",
  "keep-alive",
  "proxy-authenticate",
  "proxy-authorization",
  "te",
  "trailers",
  "transfer-encoding",
  "upgrade",
];

/** Proxy timeout in milliseconds. */
export const PROXY_TIMEOUT_MS = 30_000;

/** True for any request path this proxy owns ("/api/v1/..." and friends). */
export function isApiProxyPath(pathname: string): boolean {
  return pathname === "/api" || pathname.startsWith("/api/");
}

/**
 * Validates the backend-relative path (already stripped of the leading
 * "/api/") -- only allow v1/-prefixed paths, no traversal. Checks both
 * literal and URL-encoded traversal sequences. Split out from the request
 * handler below so the SSRF guard itself is unit-testable without a real
 * IncomingMessage/ServerResponse pair.
 */
export function isValidBackendPath(backendPath: string): boolean {
  return backendPath.startsWith("v1/") && !/\.\.|\/\/|%2e/i.test(backendPath);
}

/** Matches the two OIDC callback paths whose 302 the browser must follow itself. */
export function isOIDCRedirectPath(backendPath: string): boolean {
  return /^v1\/auth\/oidc\/[^/]+\/(login|callback)$/.test(backendPath);
}

function splitUrl(url: string): { pathname: string; search: string } {
  const qIndex = url.indexOf("?");
  return qIndex === -1
    ? { pathname: url, search: "" }
    : { pathname: url.slice(0, qIndex), search: url.slice(qIndex) };
}

function sendJson(res: ServerResponse, status: number, body: unknown): void {
  const payload = JSON.stringify(body);
  res.statusCode = status;
  res.setHeader("Content-Type", "application/json");
  res.end(payload);
}

export interface ApiProxyOptions {
  /** Overridable for tests; defaults to BACKEND_URL / http://localhost:8080. */
  backendUrl?: string;
  /** Overridable for tests so the 504 scenario doesn't take 30 real seconds. */
  timeoutMs?: number;
}

/**
 * Handles one request under /api/ (excluding the token-exchange endpoint,
 * which dispatch.ts routes separately -- see oidc-token-exchange.ts).
 * Always fully handles the request (never falls through), matching the
 * Fresh catch-all route it replaces.
 */
export async function handleApiProxy(
  req: IncomingMessage,
  res: ServerResponse,
  options: ApiProxyOptions = {},
): Promise<void> {
  const backendUrl = options.backendUrl ?? BACKEND_URL;
  const timeoutMs = options.timeoutMs ?? PROXY_TIMEOUT_MS;

  const { pathname, search } = splitUrl(req.url ?? "/");
  const backendPath = pathname.startsWith("/api/")
    ? pathname.slice("/api/".length)
    : "";

  // Validate path to prevent SSRF -- only allow v1/ prefixed paths, no traversal.
  if (!isValidBackendPath(backendPath)) {
    sendJson(res, 400, { error: { code: 400, message: "Invalid API path" } });
    return;
  }

  const target = `${backendUrl}/api/${backendPath}${search}`;

  // Build allowlisted headers only. IncomingMessage.headers keys are
  // already lowercased and duplicate non-set-cookie headers already
  // comma-joined by Node's HTTP parser, so a differently-cased or
  // duplicated inbound header is still matched here.
  const headers = new Headers();
  for (const name of FORWARD_HEADERS) {
    const value = req.headers[name];
    if (typeof value === "string" && value.length > 0) {
      headers.set(name, value);
    } else if (Array.isArray(value) && value.length > 0) {
      headers.set(name, value.join(", "));
    }
  }

  // OIDC callback paths return HTTP 302 redirects that the browser must
  // follow directly. redirect: "manual" prevents fetch() from following
  // them server-side (which would swallow the redirect and return the
  // HTML page).
  const manualRedirect = isOIDCRedirectPath(backendPath);

  const method = (req.method ?? "GET").toUpperCase();
  const hasBody = method !== "GET" && method !== "HEAD";

  try {
    const backendRes = await fetch(target, {
      method,
      headers,
      // Fetch (and Node's http client under the hood) refuses a body on
      // GET/HEAD, so only attach one for methods that can carry one.
      // node:stream/web's ReadableStream and the DOM lib's ReadableStream
      // are structurally close but not assignable to each other under
      // strict TS; fetch() accepts either at runtime.
      body: hasBody
        ? (Readable.toWeb(req) as unknown as ReadableStream)
        : undefined,
      redirect: manualRedirect ? "manual" : "follow",
      signal: AbortSignal.timeout(timeoutMs),
    });

    res.statusCode = backendRes.status;
    for (const [name, value] of backendRes.headers) {
      const lower = name.toLowerCase();
      if (lower === "set-cookie" || HOP_BY_HOP_HEADERS.includes(lower)) {
        continue;
      }
      res.setHeader(name, value);
    }
    // Fetch's Headers iterator folds repeated Set-Cookie into one comma-
    // joined value, which corrupts cookie attributes -- getSetCookie()
    // (Node 18.14+/Bun) preserves them as separate values.
    const setCookieHeaders =
      typeof backendRes.headers.getSetCookie === "function"
        ? backendRes.headers.getSetCookie()
        : [];
    if (setCookieHeaders.length > 0) {
      res.setHeader("set-cookie", setCookieHeaders);
    }

    if (!backendRes.body) {
      res.end();
      return;
    }

    await new Promise<void>((resolve, reject) => {
      // biome-ignore lint/suspicious/noExplicitAny: Node's Readable.fromWeb types don't line up 1:1 with the DOM ReadableStream type used above.
      const nodeStream = Readable.fromWeb(backendRes.body as any);
      nodeStream.pipe(res);
      nodeStream.on("error", reject);
      res.on("finish", resolve);
      res.on("close", resolve);
      res.on("error", reject);
    });
  } catch (err) {
    // Don't expose the internal backend URL in logs or responses.
    const isTimeout =
      err instanceof DOMException && err.name === "TimeoutError";
    console.error(
      "Proxy error:",
      isTimeout
        ? "backend timeout"
        : err instanceof Error
          ? err.message
          : "unknown error",
    );

    if (res.headersSent) {
      // Streaming had already started when the backend died mid-response;
      // nothing left to do but end the connection.
      res.end();
      return;
    }

    if (isTimeout) {
      sendJson(res, 504, {
        error: {
          code: 504,
          message: "Gateway timeout",
          detail: "The backend did not respond in time",
        },
      });
      return;
    }

    sendJson(res, 502, {
      error: {
        code: 502,
        message: "Backend unavailable",
        detail: "Could not connect to the k8sCenter backend",
      },
    });
  }
}
