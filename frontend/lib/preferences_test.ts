import { assertEquals } from "jsr:@std/assert@1";
import { ApiError } from "./api.ts";
import { PREFERENCE_REASONS, preferenceReason } from "./preferences.ts";

// preferenceReason is the one piece of lib/preferences.ts that has no network
// seam, so it is the one piece that is unit-testable. The rest of the module
// is transport over lib/api.ts, which has no injectable fetch; those methods
// are covered end-to-end by the UI units that consume them.
//
// Importing lib/preferences.ts here is safe despite its client-only banner:
// the banner is about SSR request handling, where the module-level auth token
// in lib/api.ts would be shared across requests. Under `deno test` there is no
// request, and lib/cluster.ts gates its only side effect behind IS_BROWSER.

function apiErrorWithReason(reason: string): ApiError {
  return new ApiError(409, 409, "conflict", {
    error: { code: 409, message: "conflict", reason },
  });
}

Deno.test("preferenceReason: returns every reason the server can emit", () => {
  for (const reason of PREFERENCE_REASONS) {
    assertEquals(preferenceReason(apiErrorWithReason(reason)), reason);
  }
});

Deno.test("preferenceReason: an unrecognized reason is undefined, not passed through", () => {
  // A reason code this build does not know must not reach a caller that would
  // switch on it. Undefined means "we cannot name this failure", which the
  // caller is required to say out loud.
  assertEquals(preferenceReason(apiErrorWithReason("teapot")), undefined);
});

Deno.test("preferenceReason: an error carrying no reason is undefined", () => {
  assertEquals(preferenceReason(new ApiError(500, 500, "boom")), undefined);
});

Deno.test("preferenceReason: a non-ApiError is undefined", () => {
  // The shapes a caller actually sees when the request never reached the
  // server: a dropped connection, and an aborted in-flight request.
  assertEquals(preferenceReason(new TypeError("Failed to fetch")), undefined);
  assertEquals(
    preferenceReason(new DOMException("Aborted", "AbortError")),
    undefined,
  );
  assertEquals(preferenceReason(undefined), undefined);
  assertEquals(preferenceReason(null), undefined);
  assertEquals(preferenceReason("database_unavailable"), undefined);
});

Deno.test("PREFERENCE_REASONS covers the handler's reason codes", () => {
  // Pinned to the WriteErrorWithReason call sites in
  // backend/internal/preferences/handler.go and types.go. Adding a reason
  // there without adding it here means the UI silently stops classifying it.
  assertEquals([...PREFERENCE_REASONS].sort(), [
    "already_pinned",
    "database_unavailable",
    "duplicate_name",
    "identity_too_long",
    "invalid_config",
    "invalid_name",
    "limit_reached",
    "revision_conflict",
    "unknown_resource_kind",
    "unsupported_schema_version",
  ]);
});
