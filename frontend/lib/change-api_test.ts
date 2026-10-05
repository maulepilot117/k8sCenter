import { afterEach, expect, test } from "bun:test";
import { ApiError, errorExtra, setAccessToken } from "./api.ts";
import {
  buildReceiptListQuery,
  getReceipt,
  getVerification,
  listReceipts,
  resolveOwnership,
} from "./change-api.ts";
import type {
  OwnershipResponse,
  ReceiptDetail,
  ReceiptView,
  VerificationView,
} from "./change-types.ts";
import { switchCluster } from "./cluster.ts";

// The client is a thin typing layer over api.ts, so what is worth pinning is
// the request it puts on the wire (path, method, body, X-Cluster-ID, signal)
// and the way it unwraps the envelope. Only fetch is replaced.

interface Recorded {
  url: string;
  method: string;
  clusterHeader: string | null;
  signal: AbortSignal | null | undefined;
  body: string | null;
}

const ID = "6f1d3c52-4b1e-4f0a-9c53-0d7a2b8e1f64";

let calls: Recorded[] = [];
let original: typeof globalThis.fetch | undefined;

function stubFetch(status: number, payload: unknown) {
  calls = [];
  // A test may stub more than once; only the first capture is the real fetch.
  original ??= globalThis.fetch;
  globalThis.fetch = ((input: string | URL | Request, init?: RequestInit) => {
    calls.push({
      url: String(input),
      method: init?.method ?? "GET",
      clusterHeader: new Headers(init?.headers).get("X-Cluster-ID"),
      signal: init?.signal,
      body: typeof init?.body === "string" ? init.body : null,
    });
    return Promise.resolve(
      new Response(JSON.stringify(payload), {
        status,
        headers: { "Content-Type": "application/json" },
      }),
    );
  }) as typeof globalThis.fetch;
}

afterEach(() => {
  if (original) globalThis.fetch = original;
  original = undefined;
  setAccessToken(null);
});

function view(operationId: string): ReceiptView {
  return {
    operationId,
    receiptUrl: `/v1/changes/${operationId}`,
    ownerUsername: "alice",
    state: "applied",
    clusterId: "cluster-remote",
    clusterGeneration: "gen-1",
    targetGenerationChanged: false,
    contentDigest: "sha256:abc",
    documentCount: 1,
    recordedThrough: 1,
    force: false,
    containsSecret: false,
    verification: {
      state: "pending",
      url: `/v1/changes/${operationId}/verification`,
    },
    createdAt: "2026-09-10T10:00:00Z",
  };
}

test("buildReceiptListQuery: empty, page, pageSize, both", () => {
  expect(buildReceiptListQuery()).toBe("");
  expect(buildReceiptListQuery({})).toBe("");
  expect(buildReceiptListQuery({ page: 2 })).toBe("?page=2");
  expect(buildReceiptListQuery({ pageSize: 25 })).toBe("?pageSize=25");
  expect(buildReceiptListQuery({ pageSize: 25, page: 0 })).toBe(
    "?page=0&pageSize=25",
  );
});

test("listReceipts requests the page and unwraps items and metadata", async () => {
  switchCluster("cluster-selected", "gen-s");
  stubFetch(200, {
    data: [view(ID)],
    metadata: { total: 41, page: 2, pageSize: 20 },
  });

  const page = await listReceipts({ page: 2, pageSize: 20 });

  expect(calls).toHaveLength(1);
  expect(calls[0].url).toBe("/api/v1/changes?page=2&pageSize=20");
  expect(calls[0].method).toBe("GET");
  expect(page.items).toEqual([view(ID)]);
  expect(page.total).toBe(41);
  expect(page.page).toBe(2);
  expect(page.pageSize).toBe(20);
});

test("listReceipts with no params sends no query and tolerates no metadata", async () => {
  stubFetch(200, { data: [view(ID)] });

  const page = await listReceipts();

  expect(calls[0].url).toBe("/api/v1/changes");
  expect(page.total).toBe(1);
  expect(page.page).toBeUndefined();
});

test("receipt reads are not bound to the selected cluster", async () => {
  switchCluster("cluster-selected", "gen-s");
  const detail = { ...view(ID), access: "owner" } as ReceiptDetail;
  stubFetch(200, { data: detail });

  await getReceipt(ID);
  await getVerification(ID);
  await listReceipts();

  expect(calls.map((c) => c.clusterHeader)).toEqual([
    "local",
    "local",
    "local",
  ]);
});

test("getReceipt fetches one receipt by encoded id", async () => {
  const detail = {
    ...view(ID),
    access: "owner",
    summary: {
      total: 1,
      created: 1,
      configured: 0,
      unchanged: 0,
      failed: 0,
      notRecorded: 0,
    },
    objects: [],
    redactedObjects: 0,
    checks: [],
    redactedChecks: 0,
    ownership: [],
    redactedOwnership: 0,
  } satisfies ReceiptDetail;
  stubFetch(200, { data: detail });

  const got = await getReceipt(ID);
  expect(calls[0].url).toBe(`/api/v1/changes/${ID}`);
  expect(got).toEqual(detail);

  stubFetch(200, { data: detail });
  await getReceipt("a/b?c");
  expect(calls[0].url).toBe("/api/v1/changes/a%2Fb%3Fc");
});

test("getVerification hits the verification path and passes the abort signal", async () => {
  const verification: VerificationView = {
    state: "verifying",
    checks: [],
    redactedChecks: 0,
    retryAfterSeconds: 3,
  };
  stubFetch(200, { data: verification });
  const controller = new AbortController();

  const got = await getVerification(ID, controller.signal);

  expect(calls[0].url).toBe(`/api/v1/changes/${ID}/verification`);
  expect(calls[0].signal).toBe(controller.signal);
  expect(got.retryAfterSeconds).toBe(3);
});

test("getReceipt passes the abort signal and rejects when it is aborted", async () => {
  const controller = new AbortController();
  calls = [];
  original ??= globalThis.fetch;
  globalThis.fetch = ((_input: string | URL | Request, init?: RequestInit) => {
    calls.push({
      url: "",
      method: "GET",
      clusterHeader: null,
      signal: init?.signal,
      body: null,
    });
    return init?.signal?.aborted
      ? Promise.reject(new DOMException("Aborted", "AbortError"))
      : new Promise<Response>(() => {});
  }) as typeof globalThis.fetch;

  controller.abort();
  await expect(getReceipt(ID, controller.signal)).rejects.toThrow();
  expect(calls[0].signal).toBe(controller.signal);
});

test("resolveOwnership posts the objects to the named cluster", async () => {
  switchCluster("cluster-selected", "gen-s");
  const response: OwnershipResponse = { clusterId: "cluster-a", results: [] };
  stubFetch(200, { data: response });
  const controller = new AbortController();

  const got = await resolveOwnership(
    "cluster-a",
    [
      {
        group: "apps",
        version: "v1",
        kind: "Deployment",
        namespace: "web",
        name: "api",
      },
      { kind: "Namespace", name: "web" },
    ],
    controller.signal,
  );

  expect(calls).toHaveLength(1);
  expect(calls[0].url).toBe("/api/v1/changes/ownership");
  expect(calls[0].method).toBe("POST");
  // The named cluster, not the selection, is the header and the body.
  expect(calls[0].clusterHeader).toBe("cluster-a");
  expect(JSON.parse(calls[0].body ?? "null")).toEqual({
    clusterId: "cluster-a",
    objects: [
      {
        group: "apps",
        version: "v1",
        kind: "Deployment",
        namespace: "web",
        name: "api",
      },
      { kind: "Namespace", name: "web" },
    ],
  });
  expect(calls[0].signal).toBe(controller.signal);
  expect(got).toEqual(response);
});

test("errors surface through ApiError.reason and errorExtra", async () => {
  stubFetch(409, {
    error: {
      code: 409,
      message: "refused",
      reason: "operation_in_flight",
      extra: { receiptId: ID },
    },
  });

  const err = await getReceipt(ID).catch((e: unknown) => e);

  expect(err).toBeInstanceOf(ApiError);
  expect((err as ApiError).status).toBe(409);
  expect((err as ApiError).reason).toBe("operation_in_flight");
  expect(errorExtra(err as ApiError, "receiptId")).toBe(ID);
});

test("a receipt the caller may not read is a plain 404", async () => {
  stubFetch(404, { error: { code: 404, message: "not found" } });

  const err = await getReceipt(ID).catch((e: unknown) => e);

  expect(err).toBeInstanceOf(ApiError);
  expect((err as ApiError).status).toBe(404);
});
