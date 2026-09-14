import { afterEach, expect, test } from "bun:test";
import { createServer, type Server } from "node:http";
import { applySecurityHeaders, SECURITY_HEADERS } from "./headers.ts";
import {
  handleOIDCTokenExchange,
  OIDC_TOKEN_EXCHANGE_PATH,
  parseCookies,
} from "./oidc-token-exchange.ts";

type Cleanup = () => void;
const cleanups: Cleanup[] = [];
afterEach(() => {
  while (cleanups.length) cleanups.pop()?.();
});

function startServer(): Promise<{ url: string; close: () => void }> {
  return new Promise((resolvePromise) => {
    const server: Server = createServer((req, res) => {
      applySecurityHeaders(res);
      handleOIDCTokenExchange(req, res);
    });
    server.listen(0, "127.0.0.1", () => {
      const port = (server.address() as { port: number }).port;
      resolvePromise({
        url: `http://127.0.0.1:${port}`,
        close: () => server.close(),
      });
    });
  });
}

test("OIDC_TOKEN_EXCHANGE_PATH matches the Fresh route it ports", () => {
  expect(OIDC_TOKEN_EXCHANGE_PATH).toBe("/api/auth/oidc-token-exchange");
});

test("parseCookies splits a Cookie header into a name/value map", () => {
  expect(parseCookies("a=1; b=2; oidc_access_token=xyz")).toEqual({
    a: "1",
    b: "2",
    oidc_access_token: "xyz",
  });
});

test("parseCookies handles an empty header", () => {
  expect(parseCookies("")).toEqual({});
});

test("refuses a request missing X-Requested-With with 403 and leaves the cookie intact", async () => {
  const server = await startServer();
  cleanups.push(server.close);

  const res = await fetch(`${server.url}${OIDC_TOKEN_EXCHANGE_PATH}`, {
    method: "POST",
    headers: { cookie: "oidc_access_token=abc123" },
  });

  expect(res.status).toBe(403);
  const body = await res.json();
  expect(body.error.code).toBe(403);
  // No Set-Cookie was issued -- the cookie the client already holds is
  // untouched (the client simply keeps sending it).
  expect(res.headers.get("set-cookie")).toBeNull();
});

test("accepts a request with X-Requested-With, returning the token and clearing the cookie", async () => {
  const server = await startServer();
  cleanups.push(server.close);

  const res = await fetch(`${server.url}${OIDC_TOKEN_EXCHANGE_PATH}`, {
    method: "POST",
    headers: {
      cookie: "oidc_access_token=abc123",
      "x-requested-with": "XMLHttpRequest",
    },
  });

  expect(res.status).toBe(200);
  const body = await res.json();
  expect(body.data.accessToken).toBe("abc123");

  const setCookie = res.headers.get("set-cookie") ?? "";
  expect(setCookie).toContain("oidc_access_token=;");
  expect(setCookie).toContain("Path=/api/auth/oidc-token-exchange");
  expect(setCookie).toContain("HttpOnly");
  expect(setCookie).toContain("SameSite=Lax");
  expect(setCookie).toContain("Max-Age=0");
});

test("refuses an immediate replay with the cleared cookie, proving single-use", async () => {
  const server = await startServer();
  cleanups.push(server.close);

  const first = await fetch(`${server.url}${OIDC_TOKEN_EXCHANGE_PATH}`, {
    method: "POST",
    headers: {
      cookie: "oidc_access_token=abc123",
      "x-requested-with": "XMLHttpRequest",
    },
  });
  expect(first.status).toBe(200);

  // The browser would now hold the cleared cookie (empty value, Max-Age=0).
  // A replay with no cookie at all -- the realistic post-clear state --
  // must be refused.
  const replay = await fetch(`${server.url}${OIDC_TOKEN_EXCHANGE_PATH}`, {
    method: "POST",
    headers: { "x-requested-with": "XMLHttpRequest" },
  });
  expect(replay.status).toBe(401);
  const body = await replay.json();
  expect(body.error.code).toBe(401);
});

test("returns 401 when no oidc_access_token cookie is present at all", async () => {
  const server = await startServer();
  cleanups.push(server.close);

  const res = await fetch(`${server.url}${OIDC_TOKEN_EXCHANGE_PATH}`, {
    method: "POST",
    headers: { "x-requested-with": "XMLHttpRequest" },
  });
  expect(res.status).toBe(401);
});

test("the five security headers are present on the 401 response", async () => {
  const server = await startServer();
  cleanups.push(server.close);
  const res = await fetch(`${server.url}${OIDC_TOKEN_EXCHANGE_PATH}`, {
    method: "POST",
    headers: { "x-requested-with": "XMLHttpRequest" },
  });
  expect(res.status).toBe(401);
  for (const [name] of SECURITY_HEADERS) {
    expect(res.headers.get(name.toLowerCase())).not.toBeNull();
  }
});

test("the five security headers are present on the 403 response", async () => {
  const server = await startServer();
  cleanups.push(server.close);
  const res = await fetch(`${server.url}${OIDC_TOKEN_EXCHANGE_PATH}`, {
    method: "POST",
  });
  expect(res.status).toBe(403);
  for (const [name] of SECURITY_HEADERS) {
    expect(res.headers.get(name.toLowerCase())).not.toBeNull();
  }
});

test("the five security headers are present on the 200 success response", async () => {
  const server = await startServer();
  cleanups.push(server.close);
  const res = await fetch(`${server.url}${OIDC_TOKEN_EXCHANGE_PATH}`, {
    method: "POST",
    headers: {
      cookie: "oidc_access_token=abc123",
      "x-requested-with": "XMLHttpRequest",
    },
  });
  expect(res.status).toBe(200);
  for (const [name] of SECURITY_HEADERS) {
    expect(res.headers.get(name.toLowerCase())).not.toBeNull();
  }
});
