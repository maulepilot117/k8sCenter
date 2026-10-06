import { afterEach, expect, test } from "bun:test";
import { ApiError, api, apiBlob, setAccessToken } from "./api.ts";

// apiBlob shares api()'s transport: the bearer token, X-Cluster-ID, the
// single 401 refresh-and-retry and ApiError parsing. It differs only in what
// a success returns (the body as a Blob, plus the response headers), which is
// what a file download needs.

interface Recorded {
  url: string;
  method: string;
  headers: Headers;
}

let calls: Recorded[] = [];
let original: typeof globalThis.fetch | undefined;

function stubFetch(responses: Array<() => Response>) {
  calls = [];
  original = globalThis.fetch;
  let i = 0;
  globalThis.fetch = ((input: string | URL | Request, init?: RequestInit) => {
    calls.push({
      url: String(input),
      method: init?.method ?? "GET",
      headers: new Headers(init?.headers),
    });
    const make = responses[i++];
    return Promise.resolve(make ? make() : new Response("{}", { status: 500 }));
  }) as typeof globalThis.fetch;
}

afterEach(() => {
  if (original) globalThis.fetch = original;
  original = undefined;
  setAccessToken(null);
});

const file = () =>
  new Response("# incident", {
    status: 200,
    headers: {
      "Content-Type": "text/markdown",
      "Content-Disposition": 'attachment; filename="x.md"',
    },
  });
const unauthorized = () => new Response("{}", { status: 401 });
const refreshed = () =>
  new Response(JSON.stringify({ data: { accessToken: "fresh" } }), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });

test("apiBlob sends the bearer token and cluster, and returns body and headers", async () => {
  setAccessToken("tok");
  stubFetch([file]);
  const res = await apiBlob("/v1/things/1/export", { clusterId: "local" });
  expect(calls).toHaveLength(1);
  expect(calls[0].url).toBe("/api/v1/things/1/export");
  expect(calls[0].method).toBe("GET");
  expect(calls[0].headers.get("Authorization")).toBe("Bearer tok");
  expect(calls[0].headers.get("X-Cluster-ID")).toBe("local");
  expect(res.status).toBe(200);
  expect(res.headers.get("Content-Disposition")).toBe(
    'attachment; filename="x.md"',
  );
  expect(await res.blob.text()).toBe("# incident");
});

test("apiBlob refreshes the token once on a 401 and retries with the new one", async () => {
  setAccessToken("stale");
  stubFetch([unauthorized, refreshed, file]);
  const res = await apiBlob("/v1/things/1/export");
  expect(calls.map((c) => c.url)).toEqual([
    "/api/v1/things/1/export",
    "/api/v1/auth/refresh",
    "/api/v1/things/1/export",
  ]);
  expect(calls[0].headers.get("Authorization")).toBe("Bearer stale");
  expect(calls[2].headers.get("Authorization")).toBe("Bearer fresh");
  expect(await res.blob.text()).toBe("# incident");
});

test("apiBlob does not loop: a second 401 after a refresh is an ApiError 401", async () => {
  setAccessToken("stale");
  stubFetch([unauthorized, refreshed, unauthorized, file]);
  const err = await apiBlob("/v1/things/1/export").catch((e) => e);
  expect(err).toBeInstanceOf(ApiError);
  expect((err as ApiError).status).toBe(401);
  expect(calls).toHaveLength(3);
});

test("apiBlob parses an error envelope into ApiError with reason, extra and headers", async () => {
  stubFetch([
    () =>
      new Response(
        JSON.stringify({
          error: {
            code: 503,
            message: "too many exports in progress; retry shortly",
            reason: "incident_busy",
            extra: { max: 2 },
          },
        }),
        {
          status: 503,
          headers: { "Content-Type": "application/json", "Retry-After": "3" },
        },
      ),
  ]);
  const err = (await apiBlob("/v1/things/1/export").catch(
    (e) => e,
  )) as ApiError;
  expect(err).toBeInstanceOf(ApiError);
  expect(err.status).toBe(503);
  expect(err.reason).toBe("incident_busy");
  expect(err.detail).toBe("too many exports in progress; retry shortly");
  expect(err.body?.error?.extra).toEqual({ max: 2 });
  expect(err.headers?.get("Retry-After")).toBe("3");
});

// api() now runs over the same send(); these pin what it kept.

test("api() answers a 204 with an empty envelope", async () => {
  stubFetch([() => new Response(null, { status: 204 })]);
  const res = await api("/v1/things/1", { method: "DELETE" });
  expect(res).toEqual({ data: undefined } as unknown as typeof res);
  expect(calls[0].headers.get("X-Requested-With")).toBe("XMLHttpRequest");
});

test("api() turns a non-JSON error body into an ApiError with the status text", async () => {
  stubFetch([
    () =>
      new Response("<html>bad gateway</html>", {
        status: 502,
        statusText: "Bad Gateway",
        headers: { "Content-Type": "text/html" },
      }),
  ]);
  const err = (await api("/v1/things").catch((e) => e)) as ApiError;
  expect(err).toBeInstanceOf(ApiError);
  expect(err.status).toBe(502);
  expect(err.code).toBe(502);
  expect(err.detail).toBe("Bad Gateway");
  expect(err.reason).toBeUndefined();
});

test("api() errors carry the response headers too", async () => {
  stubFetch([
    () =>
      new Response(
        JSON.stringify({
          error: { code: 503, message: "busy", reason: "incident_busy" },
        }),
        { status: 503, headers: { "Retry-After": "2" } },
      ),
  ]);
  const err = (await api("/v1/things").catch((e) => e)) as ApiError;
  expect(err.reason).toBe("incident_busy");
  expect(err.headers?.get("Retry-After")).toBe("2");
});
