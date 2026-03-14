import { define } from "@/utils.ts";
import { BACKEND_URL } from "@/lib/constants.ts";

/** Headers to forward from client to backend. */
const FORWARD_HEADERS = [
  "authorization",
  "content-type",
  "accept",
  "x-requested-with",
  "x-cluster-id",
  "cookie",
];

/** Hop-by-hop headers to strip from backend responses. */
const HOP_BY_HOP_HEADERS = [
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
const PROXY_TIMEOUT_MS = 30_000;

/**
 * Catch-all BFF proxy to the Go backend.
 * - Only forwards allowlisted headers
 * - Validates path to prevent SSRF
 * - Streams response body for SSE/large responses
 */
export const handler = define.handlers({
  async GET(ctx) {
    return await proxyRequest(ctx);
  },
  async POST(ctx) {
    return await proxyRequest(ctx);
  },
  async PUT(ctx) {
    return await proxyRequest(ctx);
  },
  async DELETE(ctx) {
    return await proxyRequest(ctx);
  },
  async PATCH(ctx) {
    return await proxyRequest(ctx);
  },
});

async function proxyRequest(
  ctx: { req: Request; params: { path: string } },
): Promise<Response> {
  const backendPath = ctx.params.path;

  // Validate path to prevent SSRF — only allow v1/ prefixed paths, no traversal
  // Check both literal and URL-encoded traversal sequences
  if (
    !backendPath.startsWith("v1/") ||
    /\.\.|\/\/|%2e/i.test(backendPath)
  ) {
    return new Response(
      JSON.stringify({
        error: { code: 400, message: "Invalid API path" },
      }),
      { status: 400, headers: { "Content-Type": "application/json" } },
    );
  }

  const url = new URL(ctx.req.url);
  const target = `${BACKEND_URL}/api/${backendPath}${url.search}`;

  // Build allowlisted headers only
  const headers = new Headers();
  for (const name of FORWARD_HEADERS) {
    const value = ctx.req.headers.get(name);
    if (value) {
      headers.set(name, value);
    }
  }

  // OIDC callback paths return HTTP 302 redirects that the browser must follow
  // directly. Using redirect: "manual" prevents fetch() from following them
  // server-side (which would swallow the redirect and return the HTML page).
  const isOIDCPath = /^v1\/auth\/oidc\/[^/]+\/(login|callback)$/.test(
    backendPath,
  );

  try {
    const backendRes = await fetch(target, {
      method: ctx.req.method,
      headers,
      body: ctx.req.body,
      redirect: isOIDCPath ? "manual" : "follow",
      signal: AbortSignal.timeout(PROXY_TIMEOUT_MS),
      // @ts-expect-error — Deno supports duplex for streaming requests
      duplex: "half",
    });

    // Strip hop-by-hop headers from response
    const responseHeaders = new Headers(backendRes.headers);
    for (const h of HOP_BY_HOP_HEADERS) {
      responseHeaders.delete(h);
    }

    return new Response(backendRes.body, {
      status: backendRes.status,
      statusText: backendRes.statusText,
      headers: responseHeaders,
    });
  } catch (err) {
    // Don't expose internal backend URL in logs or responses
    const isTimeout = err instanceof DOMException &&
      err.name === "TimeoutError";
    console.error(
      "Proxy error:",
      isTimeout
        ? "backend timeout"
        : (err instanceof Error ? err.message : "unknown error"),
    );

    if (isTimeout) {
      return new Response(
        JSON.stringify({
          error: {
            code: 504,
            message: "Gateway timeout",
            detail: "The backend did not respond in time",
          },
        }),
        { status: 504, headers: { "Content-Type": "application/json" } },
      );
    }

    return new Response(
      JSON.stringify({
        error: {
          code: 502,
          message: "Backend unavailable",
          detail: "Could not connect to the KubeCenter backend",
        },
      }),
      { status: 502, headers: { "Content-Type": "application/json" } },
    );
  }
}
