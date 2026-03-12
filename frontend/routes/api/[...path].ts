import { define } from "@/utils.ts";
import { BACKEND_URL } from "@/lib/constants.ts";

/**
 * Catch-all BFF proxy to the Go backend.
 * Forwards all headers including Authorization.
 * Streams response body for SSE/large responses.
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
  const url = new URL(ctx.req.url);
  const target = `${BACKEND_URL}/api/${backendPath}${url.search}`;

  const headers = new Headers(ctx.req.headers);
  // Remove host header so the backend sees its own host
  headers.delete("host");

  try {
    const backendRes = await fetch(target, {
      method: ctx.req.method,
      headers,
      body: ctx.req.body,
      // @ts-ignore — Deno supports duplex for streaming
      duplex: "half",
    });

    // Stream the response back
    const responseHeaders = new Headers(backendRes.headers);
    // Remove transfer-encoding since we may re-chunk
    responseHeaders.delete("transfer-encoding");

    return new Response(backendRes.body, {
      status: backendRes.status,
      statusText: backendRes.statusText,
      headers: responseHeaders,
    });
  } catch (err) {
    console.error(`Proxy error: ${target}`, err);
    return new Response(
      JSON.stringify({
        error: {
          code: 502,
          message: "Backend unavailable",
          detail: "Could not connect to the KubeCenter backend",
        },
      }),
      {
        status: 502,
        headers: { "Content-Type": "application/json" },
      },
    );
  }
}
