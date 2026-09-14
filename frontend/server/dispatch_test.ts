import { expect, test } from "bun:test";
import { createRequestListener, rejectMalformed } from "./dispatch.ts";
import { SECURITY_HEADERS } from "./headers.ts";

function makeFakeReqRes(overrides: { method?: string; url?: string } = {}) {
  const headers = new Map<string, string>();
  let statusCode = 200;
  let ended = false;
  let endedWith: unknown;
  const req = { method: overrides.method ?? "GET", url: overrides.url ?? "/" };
  const res = {
    get statusCode() {
      return statusCode;
    },
    set statusCode(v: number) {
      statusCode = v;
    },
    setHeader(name: string, value: string) {
      headers.set(name, value);
    },
    getHeader(name: string) {
      return headers.get(name);
    },
    end(chunk?: unknown) {
      ended = true;
      endedWith = chunk;
    },
    get ended() {
      return ended;
    },
    get endedWith() {
      return endedWith;
    },
    headers,
  };
  return { req, res };
}

test("rejectMalformed rejects an unsupported method with 405, before any other branch", () => {
  const { req, res } = makeFakeReqRes({ method: "TRACE" });
  // biome-ignore lint/suspicious/noExplicitAny: minimal fakes
  expect(rejectMalformed(req as any, res as any)).toBe(true);
  expect(res.statusCode).toBe(405);
});

test("rejectMalformed rejects an oversized URL with 414", () => {
  const { req, res } = makeFakeReqRes({ url: `/${"a".repeat(9000)}` });
  // biome-ignore lint/suspicious/noExplicitAny: minimal fakes
  expect(rejectMalformed(req as any, res as any)).toBe(true);
  expect(res.statusCode).toBe(414);
});

test("rejectMalformed passes a normal GET through", () => {
  const { req, res } = makeFakeReqRes({ method: "GET", url: "/some/page" });
  // biome-ignore lint/suspicious/noExplicitAny: minimal fakes
  expect(rejectMalformed(req as any, res as any)).toBe(false);
});

test("createRequestListener sets all five headers even on a malformed-request rejection", () => {
  const listener = createRequestListener({
    astroHandler: () => {
      throw new Error("must not be called");
    },
    serveStatic: () => {
      throw new Error("must not be called");
    },
  });
  const { req, res } = makeFakeReqRes({ method: "CONNECT" });
  // biome-ignore lint/suspicious/noExplicitAny: minimal fakes
  listener(req as any, res as any);
  expect(res.statusCode).toBe(405);
  for (const [name] of SECURITY_HEADERS) {
    expect(res.headers.has(name)).toBe(true);
  }
});

test("createRequestListener answers /ws/* with 426 and headers, without touching static or astro", () => {
  const listener = createRequestListener({
    astroHandler: () => {
      throw new Error("must not be called");
    },
    serveStatic: () => {
      throw new Error("must not be called");
    },
  });
  const { req, res } = makeFakeReqRes({
    method: "GET",
    url: "/ws/v1/ws/resources",
  });
  // biome-ignore lint/suspicious/noExplicitAny: minimal fakes
  listener(req as any, res as any);
  expect(res.statusCode).toBe(426);
  expect(res.headers.has("X-Frame-Options")).toBe(true);
});

test("createRequestListener defers to serveStatic before astroHandler, and stops if it handled the request", () => {
  let astroCalled = false;
  const listener = createRequestListener({
    astroHandler: () => {
      astroCalled = true;
    },
    serveStatic: (_req, res) => {
      res.statusCode = 200;
      res.end("static-content");
      return true;
    },
  });
  const { req, res } = makeFakeReqRes({
    method: "GET",
    url: "/_astro/chunk.js",
  });
  // biome-ignore lint/suspicious/noExplicitAny: minimal fakes
  listener(req as any, res as any);
  expect(res.endedWith).toBe("static-content");
  expect(astroCalled).toBe(false);
});

test("createRequestListener falls through to astroHandler when nothing else claims the request", () => {
  let astroCalled = false;
  let seenUrl: string | undefined;
  const listener = createRequestListener({
    astroHandler: (req) => {
      astroCalled = true;
      seenUrl = req.url;
    },
    serveStatic: () => false,
  });
  const { req, res } = makeFakeReqRes({ method: "GET", url: "/" });
  // biome-ignore lint/suspicious/noExplicitAny: minimal fakes
  listener(req as any, res as any);
  expect(astroCalled).toBe(true);
  expect(seenUrl).toBe("/");
  // Headers must already be set before Astro ever runs (KTD3).
  expect(res.headers.has("Content-Security-Policy")).toBe(true);
});

test("createRequestListener rewrites the KTD11 cluster-scoped CRD path before handing off to astroHandler", () => {
  let seenUrl: string | undefined;
  const listener = createRequestListener({
    astroHandler: (req) => {
      seenUrl = req.url;
    },
    serveStatic: () => false,
  });
  const { req, res } = makeFakeReqRes({
    method: "GET",
    url: "/extensions/cert-manager.io/certificates/_/my-cert?tab=yaml",
  });
  // biome-ignore lint/suspicious/noExplicitAny: minimal fakes
  listener(req as any, res as any);
  expect(seenUrl).toBe(
    "/extensions/cert-manager.io/certificates/cluster-scoped/my-cert?tab=yaml",
  );
});

test("createRequestListener leaves an ordinary page path untouched by the KTD11 rewrite", () => {
  let seenUrl: string | undefined;
  const listener = createRequestListener({
    astroHandler: (req) => {
      seenUrl = req.url;
    },
    serveStatic: () => false,
  });
  const { req, res } = makeFakeReqRes({
    method: "GET",
    url: "/workloads/pods?namespace=default",
  });
  // biome-ignore lint/suspicious/noExplicitAny: minimal fakes
  listener(req as any, res as any);
  expect(seenUrl).toBe("/workloads/pods?namespace=default");
});
