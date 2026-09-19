import { expect, test } from "bun:test";
import { ApiError } from "./api.ts";
import type { DashboardLayoutConfig } from "./dashboard/types.ts";
import {
  PREFERENCE_REASONS,
  preferenceReason,
  preferencesApi,
} from "./preferences.ts";

// preferenceReason is the one piece of lib/preferences.ts that has no network
// seam, so it is the one piece that is unit-testable. The rest of the module
// is transport over lib/api.ts, which has no injectable fetch; those methods
// are covered end-to-end by the UI units that consume them.
//
// The two layout methods at the bottom of this file are the exception, and
// only for what they put on the wire. The layout PUT is the one call in this
// module whose body is NOT {name, ...}: the scope is the path and the server
// derives the record's name from it, so sending a name is a 400 naming the
// field. That is a mistake this repo has already made once on paper -- the
// plan's own D14 snippet sent one -- and nothing else here would catch it
// before a user did. They stub globalThis.fetch rather than reaching for a
// module mock, which keeps the seam local to the test and leaves no global
// behind for the next file in the run.
//
// Importing lib/preferences.ts here is safe despite its client-only banner:
// the banner is about SSR request handling, where the module-level auth token
// in lib/api.ts would be shared across requests. Under `bun test` there is no
// request, and lib/cluster.ts gates its only side effect behind IS_BROWSER.

function apiErrorWithReason(reason: string): ApiError {
  return new ApiError(409, 409, "conflict", {
    error: { code: 409, message: "conflict", reason },
  });
}

test("preferenceReason: returns every reason the server can emit", () => {
  for (const reason of PREFERENCE_REASONS) {
    expect(preferenceReason(apiErrorWithReason(reason))).toBe(reason);
  }
});

test("preferenceReason: an unrecognized reason is undefined, not passed through", () => {
  // A reason code this build does not know must not reach a caller that would
  // switch on it. Undefined means "we cannot name this failure", which the
  // caller is required to say out loud.
  expect(preferenceReason(apiErrorWithReason("teapot"))).toBe(undefined);
});

test("preferenceReason: an error carrying no reason is undefined", () => {
  expect(preferenceReason(new ApiError(500, 500, "boom"))).toBe(undefined);
});

test("preferenceReason: a non-ApiError is undefined", () => {
  // The shapes a caller actually sees when the request never reached the
  // server: a dropped connection, and an aborted in-flight request.
  expect(preferenceReason(new TypeError("Failed to fetch"))).toBe(undefined);
  expect(preferenceReason(new DOMException("Aborted", "AbortError"))).toBe(
    undefined,
  );
  expect(preferenceReason(undefined)).toBe(undefined);
  expect(preferenceReason(null)).toBe(undefined);
  expect(preferenceReason("database_unavailable")).toBe(undefined);
});

test("PREFERENCE_REASONS covers the handler's reason codes", () => {
  // Pinned to the WriteErrorWithReason call sites in
  // backend/internal/preferences/handler.go, types.go and dashboard.go.
  // Adding a reason there without adding it here means the UI silently stops
  // classifying it.
  expect([...PREFERENCE_REASONS].sort()).toEqual([
    "already_pinned",
    "database_unavailable",
    "duplicate_name",
    "identity_too_long",
    "invalid_config",
    "invalid_name",
    "limit_reached",
    "revision_conflict",
    "unknown_resource_kind",
    "unknown_widget_id",
    "unsupported_schema_version",
  ]);
});

// ---------------------------------------------------------------------------
// Dashboard layout transport
// ---------------------------------------------------------------------------

interface CapturedRequest {
  url: string;
  method: string | undefined;
  body: string | undefined;
}

/**
 * Runs `call` with `globalThis.fetch` replaced by a recorder, and restores the
 * real one before returning however the call ends.
 *
 * `bun test` shares module state across files in one run, so a stub left
 * installed would follow every later test file. The restore is in a `finally`
 * for that reason, not for tidiness.
 */
async function captureRequest<T>(
  response: Response,
  call: () => Promise<T>,
): Promise<{ captured: CapturedRequest; result: T }> {
  const realFetch = globalThis.fetch;
  let captured: CapturedRequest | undefined;

  globalThis.fetch = ((input: unknown, init?: RequestInit) => {
    captured = {
      url: String(input),
      method: init?.method,
      body: typeof init?.body === "string" ? init.body : undefined,
    };
    return Promise.resolve(response);
  }) as typeof globalThis.fetch;

  try {
    const result = await call();
    if (captured === undefined) {
      throw new Error("expected the call to issue a request");
    }
    return { captured, result };
  } finally {
    globalThis.fetch = realFetch;
  }
}

const LAYOUT_CONFIG: DashboardLayoutConfig = {
  schemaVersion: 1,
  scope: "overview",
  columns: 12,
  items: [{ instanceId: "a", id: "cluster-health", x: 0, y: 0, w: 4, h: 4 }],
};

test("getLayout: addresses the scope and reads an unsaved one as null", async () => {
  const { captured, result } = await captureRequest(
    // 204 is "you have not customized this dashboard", which api() surfaces as
    // `data: undefined`. Null is what the store turns that into.
    new Response(null, { status: 204 }),
    () => preferencesApi.getLayout("overview"),
  );

  expect(captured.url).toBe("/api/v1/preferences/layouts/overview");
  expect(captured.method).toBe("GET");
  expect(result).toBe(null);
});

test("getLayout: a stored layout comes back with its withheld list", async () => {
  const { result } = await captureRequest(
    new Response(
      JSON.stringify({
        data: {
          id: "p1",
          kind: "dashboard_layout",
          name: "overview",
          clusterId: "local",
          schemaVersion: 1,
          revision: 4,
          config: LAYOUT_CONFIG,
          createdAt: "2026-09-19T00:00:00Z",
          updatedAt: "2026-09-19T00:00:00Z",
          withheld: ["b"],
        },
      }),
      { status: 200, headers: { "content-type": "application/json" } },
    ),
    () => preferencesApi.getLayout("overview"),
  );

  expect(result?.revision).toBe(4);
  expect(result?.withheld).toEqual(["b"]);
});

test("saveLayout: PUTs revision and config, and never a name", async () => {
  const { captured } = await captureRequest(
    new Response(
      JSON.stringify({
        data: {
          id: "p1",
          kind: "dashboard_layout",
          name: "overview",
          clusterId: "local",
          schemaVersion: 1,
          revision: 5,
          config: LAYOUT_CONFIG,
          createdAt: "2026-09-19T00:00:00Z",
          updatedAt: "2026-09-19T00:00:00Z",
        },
      }),
      { status: 200, headers: { "content-type": "application/json" } },
    ),
    () => preferencesApi.saveLayout("overview", 4, LAYOUT_CONFIG),
  );

  expect(captured.url).toBe("/api/v1/preferences/layouts/overview");
  expect(captured.method).toBe("PUT");

  const body = JSON.parse(captured.body ?? "{}");
  // Exact keys, not a subset. `decodeBody` on the Go side uses
  // DisallowUnknownFields, so an extra `name` here is a 400 the user sees as
  // "saving your dashboard failed" with nothing else to go on.
  expect(Object.keys(body).sort()).toEqual(["config", "revision"]);
  expect(body.revision).toBe(4);
  expect(body.config).toEqual(LAYOUT_CONFIG);
});

test("saveLayout: revision 0 is sent as 0, not dropped as falsy", async () => {
  // The first save of a scope claims revision 0 -- "I believe none exists".
  // JSON.stringify keeps an explicit 0, but a future `revision || undefined`
  // style guard would silently turn every first save into a malformed body.
  const { captured } = await captureRequest(
    new Response(
      JSON.stringify({
        data: {
          id: "p1",
          kind: "dashboard_layout",
          name: "overview",
          clusterId: "local",
          schemaVersion: 1,
          revision: 1,
          config: LAYOUT_CONFIG,
          createdAt: "2026-09-19T00:00:00Z",
          updatedAt: "2026-09-19T00:00:00Z",
        },
      }),
      { status: 201, headers: { "content-type": "application/json" } },
    ),
    () => preferencesApi.saveLayout("overview", 0, LAYOUT_CONFIG),
  );

  expect(JSON.parse(captured.body ?? "{}").revision).toBe(0);
});
