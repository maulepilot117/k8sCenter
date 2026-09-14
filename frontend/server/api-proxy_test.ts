import { afterEach, expect, test } from "bun:test";
import type { IncomingHttpHeaders, OutgoingHttpHeaders } from "node:http";
import { createServer, request as httpRequest, type Server } from "node:http";
import {
  type ApiProxyOptions,
  handleApiProxy,
  isOIDCRedirectPath,
  isValidBackendPath,
  PROXY_TIMEOUT_MS,
} from "./api-proxy.ts";
import { applySecurityHeaders, SECURITY_HEADERS } from "./headers.ts";

type Cleanup = () => void;
const cleanups: Cleanup[] = [];
afterEach(() => {
  while (cleanups.length) cleanups.pop()?.();
});

// --- pure path-validation helpers (no I/O) ---

test("isValidBackendPath requires the v1/ prefix", () => {
  expect(isValidBackendPath("v1/pods")).toBe(true);
  expect(isValidBackendPath("v2/pods")).toBe(false);
  expect(isValidBackendPath("pods")).toBe(false);
});

test("isValidBackendPath rejects literal and encoded traversal, and double slashes", () => {
  expect(isValidBackendPath("v1/../secret")).toBe(false);
  expect(isValidBackendPath("v1//pods")).toBe(false);
  expect(isValidBackendPath("v1/%2e%2e/secret")).toBe(false);
  expect(isValidBackendPath("v1/%2E%2E/secret")).toBe(false);
});

test("isOIDCRedirectPath matches only the login/callback OIDC paths", () => {
  expect(isOIDCRedirectPath("v1/auth/oidc/okta/login")).toBe(true);
  expect(isOIDCRedirectPath("v1/auth/oidc/okta/callback")).toBe(true);
  expect(isOIDCRedirectPath("v1/auth/oidc/okta/other")).toBe(false);
  expect(isOIDCRedirectPath("v1/pods")).toBe(false);
});

// --- integration harness: a real stub backend + a real proxy server ---

function startStubBackend(
  handler: (
    req: import("node:http").IncomingMessage,
    res: import("node:http").ServerResponse,
  ) => void,
): Promise<{ port: number; url: string; close: () => void }> {
  return new Promise((resolvePromise) => {
    const server = createServer(handler);
    server.listen(0, "127.0.0.1", () => {
      const port = (server.address() as { port: number }).port;
      resolvePromise({
        port,
        url: `http://127.0.0.1:${port}`,
        close: () => server.close(),
      });
    });
  });
}

function startProxyServer(
  options: ApiProxyOptions = {},
): Promise<{ port: number; url: string; close: () => void }> {
  return new Promise((resolvePromise) => {
    const server: Server = createServer((req, res) => {
      applySecurityHeaders(res);
      void handleApiProxy(req, res, options);
    });
    server.listen(0, "127.0.0.1", () => {
      const port = (server.address() as { port: number }).port;
      resolvePromise({
        port,
        url: `http://127.0.0.1:${port}`,
        close: () => server.close(),
      });
    });
  });
}

/** Raw node:http client -- used where the test needs to send header
 * casing or duplicate header lines that fetch's Headers class would
 * normalize away before the request ever left the process. */
function rawRequest(
  url: string,
  opts: {
    method?: string;
    headers?: OutgoingHttpHeaders;
    body?: string;
  } = {},
): Promise<{ status: number; headers: IncomingHttpHeaders; body: string }> {
  return new Promise((resolvePromise, reject) => {
    const u = new URL(url);
    const req = httpRequest(
      {
        hostname: u.hostname,
        port: u.port,
        path: u.pathname + u.search,
        method: opts.method ?? "GET",
        headers: opts.headers,
      },
      (res) => {
        const chunks: Buffer[] = [];
        res.on("data", (c) => chunks.push(c));
        res.on("end", () => {
          resolvePromise({
            status: res.statusCode ?? 0,
            headers: res.headers,
            body: Buffer.concat(chunks).toString("utf8"),
          });
        });
      },
    );
    req.on("error", reject);
    if (opts.body) req.write(opts.body);
    req.end();
  });
}

// --- forward-header allowlist ---

test("a header outside the forward allowlist is not sent to the backend", async () => {
  let seenHeaders: IncomingHttpHeaders = {};
  const backend = await startStubBackend((req, res) => {
    seenHeaders = req.headers;
    res.end("ok");
  });
  cleanups.push(backend.close);
  const proxy = await startProxyServer({ backendUrl: backend.url });
  cleanups.push(proxy.close);

  await fetch(`${proxy.url}/api/v1/pods`, {
    headers: {
      authorization: "Bearer secret-token",
      "x-not-allowlisted": "should-not-cross",
    },
  });

  expect(seenHeaders.authorization).toBe("Bearer secret-token");
  expect(seenHeaders["x-not-allowlisted"]).toBeUndefined();
});

test("a forwarded header arriving with unexpected casing is still matched by the allowlist", async () => {
  let seenHeaders: IncomingHttpHeaders = {};
  const backend = await startStubBackend((req, res) => {
    seenHeaders = req.headers;
    res.end("ok");
  });
  cleanups.push(backend.close);
  const proxy = await startProxyServer({ backendUrl: backend.url });
  cleanups.push(proxy.close);

  const result = await rawRequest(`${proxy.url}/api/v1/pods`, {
    headers: { "X-CLUSTER-ID": "cluster-7" },
  });

  expect(result.status).toBe(200);
  expect(seenHeaders["x-cluster-id"]).toBe("cluster-7");
});

test("a duplicated forwarded header still reaches the backend rather than being dropped", async () => {
  let seenHeaders: IncomingHttpHeaders = {};
  const backend = await startStubBackend((req, res) => {
    seenHeaders = req.headers;
    res.end("ok");
  });
  cleanups.push(backend.close);
  const proxy = await startProxyServer({ backendUrl: backend.url });
  cleanups.push(proxy.close);

  await rawRequest(`${proxy.url}/api/v1/pods`, {
    // node:http sends one header line per array entry.
    headers: {
      "X-Cluster-Id": ["cluster-a", "cluster-b"] as unknown as string,
    },
  });

  expect(seenHeaders["x-cluster-id"]).toBeTruthy();
  expect(seenHeaders["x-cluster-id"]).toContain("cluster-a");
});

// --- hop-by-hop response stripping ---

test("a hop-by-hop header returned by the backend is stripped from the response", async () => {
  const backend = await startStubBackend((_req, res) => {
    res.setHeader("Proxy-Authenticate", "Basic");
    res.setHeader("X-Backend-Marker", "present");
    res.end("ok");
  });
  cleanups.push(backend.close);
  const proxy = await startProxyServer({ backendUrl: backend.url });
  cleanups.push(proxy.close);

  const res = await fetch(`${proxy.url}/api/v1/pods`);

  expect(res.headers.get("proxy-authenticate")).toBeNull();
  expect(res.headers.get("x-backend-marker")).toBe("present");
});

test("a hop-by-hop header returned with unexpected casing is still stripped", async () => {
  const backend = await startStubBackend((_req, res) => {
    res.setHeader("TE", "trailers");
    res.end("ok");
  });
  cleanups.push(backend.close);
  const proxy = await startProxyServer({ backendUrl: backend.url });
  cleanups.push(proxy.close);

  const result = await rawRequest(`${proxy.url}/api/v1/pods`);
  expect(result.headers.te).toBeUndefined();
});

// --- path validation (SSRF guard) ---

test("a path not starting with v1/ is refused with 400", async () => {
  const proxy = await startProxyServer({ backendUrl: "http://127.0.0.1:1" });
  cleanups.push(proxy.close);
  const res = await fetch(`${proxy.url}/api/v2/pods`);
  expect(res.status).toBe(400);
  const body = await res.json();
  expect(body.error.code).toBe(400);
});

test("a path containing .. is refused with 400", async () => {
  const proxy = await startProxyServer({ backendUrl: "http://127.0.0.1:1" });
  cleanups.push(proxy.close);
  const res = await fetch(`${proxy.url}/api/v1/../secret`);
  expect(res.status).toBe(400);
});

test("a path containing a double slash is refused with 400", async () => {
  const proxy = await startProxyServer({ backendUrl: "http://127.0.0.1:1" });
  cleanups.push(proxy.close);
  const result = await rawRequest(`${proxy.url}/api/v1//pods`);
  expect(result.status).toBe(400);
});

test("a path containing an encoded traversal sequence is refused with 400", async () => {
  const proxy = await startProxyServer({ backendUrl: "http://127.0.0.1:1" });
  cleanups.push(proxy.close);
  const res = await fetch(`${proxy.url}/api/v1/%2e%2e/secret`);
  expect(res.status).toBe(400);
});

// --- timeout vs connection-refused ---

test("a backend that never responds produces 504 after the timeout elapses", async () => {
  const backend = await startStubBackend(() => {
    // never call res.end()
  });
  cleanups.push(backend.close);
  const proxy = await startProxyServer({
    backendUrl: backend.url,
    timeoutMs: 150,
  });
  cleanups.push(proxy.close);

  const start = Date.now();
  const res = await fetch(`${proxy.url}/api/v1/pods`);
  const elapsed = Date.now() - start;

  expect(res.status).toBe(504);
  expect(elapsed).toBeLessThan(5000);
  const body = await res.json();
  expect(body.error.code).toBe(504);
});

test("a backend that refuses the connection produces 502", async () => {
  // Port 1 is privileged; nothing listens there in a test environment.
  const proxy = await startProxyServer({ backendUrl: "http://127.0.0.1:1" });
  cleanups.push(proxy.close);

  const res = await fetch(`${proxy.url}/api/v1/pods`);
  expect(res.status).toBe(502);
  const body = await res.json();
  expect(body.error.code).toBe(502);
});

test("PROXY_TIMEOUT_MS default matches the spec exactly", () => {
  expect(PROXY_TIMEOUT_MS).toBe(30_000);
});

// --- no backend-URL leakage ---

test("neither the 502 body nor any console.error line contains the backend URL", async () => {
  const backendUrl = "http://127.0.0.1:1";
  const proxy = await startProxyServer({ backendUrl });
  cleanups.push(proxy.close);

  const originalError = console.error;
  const logged: unknown[] = [];
  console.error = (...args: unknown[]) => {
    logged.push(args.join(" "));
  };
  try {
    const res = await fetch(`${proxy.url}/api/v1/pods`);
    const bodyText = await res.text();
    expect(bodyText).not.toContain("127.0.0.1:1");
    expect(bodyText).not.toContain(backendUrl);
    for (const line of logged) {
      expect(String(line)).not.toContain("127.0.0.1:1");
    }
  } finally {
    console.error = originalError;
  }
});

test("neither the 504 body nor any console.error line contains the backend URL", async () => {
  const backend = await startStubBackend(() => {
    // never respond
  });
  cleanups.push(backend.close);
  const proxy = await startProxyServer({
    backendUrl: backend.url,
    timeoutMs: 100,
  });
  cleanups.push(proxy.close);

  const originalError = console.error;
  const logged: unknown[] = [];
  console.error = (...args: unknown[]) => {
    logged.push(args.join(" "));
  };
  try {
    const res = await fetch(`${proxy.url}/api/v1/pods`);
    const bodyText = await res.text();
    expect(bodyText).not.toContain(backend.url);
    expect(bodyText).not.toContain(String(backend.port));
    for (const line of logged) {
      expect(String(line)).not.toContain(backend.url);
    }
  } finally {
    console.error = originalError;
  }
});

test("the cookie header never appears in a log line, even on a proxy failure", async () => {
  const originalError = console.error;
  const logged: unknown[] = [];
  console.error = (...args: unknown[]) => {
    logged.push(args.join(" "));
  };
  const proxy = await startProxyServer({ backendUrl: "http://127.0.0.1:1" });
  cleanups.push(proxy.close);
  try {
    await fetch(`${proxy.url}/api/v1/pods`, {
      headers: { cookie: "session=super-secret-cookie-value" },
    });
    for (const line of logged) {
      expect(String(line)).not.toContain("super-secret-cookie-value");
    }
  } finally {
    console.error = originalError;
  }
});

// --- OIDC manual redirect ---

test("an OIDC login path returns the backend's 302 to the browser rather than following it server-side", async () => {
  const backend = await startStubBackend((_req, res) => {
    res.statusCode = 302;
    res.setHeader("Location", "http://example.invalid/elsewhere");
    res.end();
  });
  cleanups.push(backend.close);
  const proxy = await startProxyServer({ backendUrl: backend.url });
  cleanups.push(proxy.close);

  const res = await fetch(`${proxy.url}/api/v1/auth/oidc/okta/login`, {
    redirect: "manual",
  });

  expect(res.status).toBe(302);
  expect(res.headers.get("location")).toBe("http://example.invalid/elsewhere");
});

test("a non-OIDC path follows a backend redirect rather than passing it through raw", async () => {
  const backend = await startStubBackend((req, res) => {
    if (req.url === "/api/v1/redirect-source") {
      res.statusCode = 302;
      res.setHeader("Location", "/api/v1/redirect-target");
      res.end();
      return;
    }
    res.end("final destination");
  });
  cleanups.push(backend.close);
  const proxy = await startProxyServer({ backendUrl: backend.url });
  cleanups.push(proxy.close);

  const res = await fetch(`${proxy.url}/api/v1/redirect-source`);
  expect(res.status).toBe(200);
  expect(await res.text()).toBe("final destination");
});

// --- streamed request body ---

test("a streamed request body reaches the backend intact", async () => {
  let received = "";
  const backend = await startStubBackend((req, res) => {
    const chunks: Buffer[] = [];
    req.on("data", (c) => chunks.push(c));
    req.on("end", () => {
      received = Buffer.concat(chunks).toString("utf8");
      res.end("received");
    });
  });
  cleanups.push(backend.close);
  const proxy = await startProxyServer({ backendUrl: backend.url });
  cleanups.push(proxy.close);

  const encoder = new TextEncoder();
  const parts = ["chunk-one-", "chunk-two-", "chunk-three"];
  const stream = new ReadableStream<Uint8Array>({
    async start(controller) {
      for (const part of parts) {
        controller.enqueue(encoder.encode(part));
        await new Promise((r) => setTimeout(r, 5));
      }
      controller.close();
    },
  });

  const res = await fetch(`${proxy.url}/api/v1/echo`, {
    method: "POST",
    body: stream,
  });

  expect(res.status).toBe(200);
  expect(received).toBe(parts.join(""));
});

// --- security headers on handler-constructed error responses ---

test("the five security headers are present on a proxy pass-through response", async () => {
  const backend = await startStubBackend((_req, res) => {
    res.end("ok");
  });
  cleanups.push(backend.close);
  const proxy = await startProxyServer({ backendUrl: backend.url });
  cleanups.push(proxy.close);

  const res = await fetch(`${proxy.url}/api/v1/pods`);
  for (const [name] of SECURITY_HEADERS) {
    expect(res.headers.get(name.toLowerCase())).not.toBeNull();
  }
});

test("the five security headers are present on the handler's own 400 response", async () => {
  const proxy = await startProxyServer({ backendUrl: "http://127.0.0.1:1" });
  cleanups.push(proxy.close);
  const res = await fetch(`${proxy.url}/api/foo`);
  expect(res.status).toBe(400);
  for (const [name] of SECURITY_HEADERS) {
    expect(res.headers.get(name.toLowerCase())).not.toBeNull();
  }
});

test("the five security headers are present on the handler's own 502 response", async () => {
  const proxy = await startProxyServer({ backendUrl: "http://127.0.0.1:1" });
  cleanups.push(proxy.close);
  const res = await fetch(`${proxy.url}/api/v1/pods`);
  expect(res.status).toBe(502);
  for (const [name] of SECURITY_HEADERS) {
    expect(res.headers.get(name.toLowerCase())).not.toBeNull();
  }
});

test("the five security headers are present on the handler's own 504 response", async () => {
  const backend = await startStubBackend(() => {
    // never respond
  });
  cleanups.push(backend.close);
  const proxy = await startProxyServer({
    backendUrl: backend.url,
    timeoutMs: 100,
  });
  cleanups.push(proxy.close);
  const res = await fetch(`${proxy.url}/api/v1/pods`);
  expect(res.status).toBe(504);
  for (const [name] of SECURITY_HEADERS) {
    expect(res.headers.get(name.toLowerCase())).not.toBeNull();
  }
});
