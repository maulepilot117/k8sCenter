import { assertEquals, assertStringIncludes } from "jsr:@std/assert@1";
import {
  applyViewState,
  captureViewState,
  classifyPin,
  isSupportedPinSchema,
  isSupportedSavedViewSchema,
  MAX_PINS,
  MAX_RECORD_NAME_LEN,
  MAX_SAVED_VIEWS,
  MAX_SEARCH_LEN,
  PIN_SCHEMA_VERSION,
  type PinConfig,
  pinDedupKey,
  SAVED_VIEW_SCHEMA_VERSION,
  SAVED_VIEW_SORT_DIRS,
  SAVED_VIEW_SORT_KEYS,
  SAVED_VIEW_STATUS_FILTERS,
  type SavedViewConfig,
  type TableViewState,
} from "./preference-types.ts";

// Pure-contract tests for the preference module. The allowlists here are a
// copy of backend/internal/preferences/types.go, and the degradation paths are
// the only place a stored record that drifted out of those allowlists gets
// explained to the user — silence there would present a coerced value as the
// one they saved (R3).

function viewState(overrides: Partial<TableViewState> = {}): TableViewState {
  return {
    resourceKind: "pods",
    namespace: "default",
    search: "nginx",
    statusFilter: "running",
    sortKey: "age",
    sortDir: "desc",
    ...overrides,
  };
}

function savedView(overrides: Partial<SavedViewConfig> = {}): SavedViewConfig {
  return {
    ...captureViewState(viewState()),
    ...overrides,
  } as SavedViewConfig;
}

function pin(overrides: Partial<PinConfig> = {}): PinConfig {
  return {
    schemaVersion: 1,
    resourceKind: "deployments",
    group: "",
    version: "",
    namespace: "default",
    name: "api",
    uid: "11111111-2222-3333-4444-555555555555",
    displayKind: "Deployment",
    ...overrides,
  };
}

Deno.test("captureViewState: round-trips the four supported table signals", () => {
  const state = viewState();
  const { state: restored, warnings } = applyViewState(captureViewState(state));
  assertEquals(restored, state);
  assertEquals(warnings, []);
});

Deno.test('captureViewState: namespace "all" is stored as empty string', () => {
  // ResourceTable's `ns` computed already collapses the "all" sentinel to ""
  // before it reaches here, and "" is what the server stores for the
  // all-namespaces scope. This pins that the capture side does not reintroduce
  // the sentinel, which the server would reject as an invalid namespace.
  const captured = captureViewState(viewState({ namespace: "" }));
  assertEquals(captured.namespace, "");
  assertEquals(applyViewState(captured).state.namespace, "");
});

Deno.test("applyViewState: a cluster-scoped view restores an empty namespace", () => {
  // Mirrors the server's validateScope rule: a cluster-scoped kind must carry
  // no namespace. Restoring one would make the table request a namespaced URL
  // for a kind that has no namespaces.
  const stored = savedView({ resourceKind: "nodes", namespace: "" });
  const { state, warnings } = applyViewState(stored);
  assertEquals(state.namespace, "");
  assertEquals(warnings, []);
});

Deno.test("captureViewState: stamps the current schema version", () => {
  assertEquals(
    captureViewState(viewState()).schemaVersion,
    SAVED_VIEW_SCHEMA_VERSION,
  );
});

Deno.test("applyViewState: unsupported sortKey degrades to name and reports a warning", () => {
  const { state, warnings } = applyViewState(
    savedView({ sortKey: "cpu" as SavedViewConfig["sortKey"] }),
  );
  assertEquals(state.sortKey, "name");
  assertEquals(warnings.length, 1);
  // The message must name the offending value and the field it degraded.
  // Asserting only the count would pass on a warning about the wrong field.
  assertStringIncludes(warnings[0], "sort key");
  assertStringIncludes(warnings[0], "cpu");
});

Deno.test("applyViewState: unknown statusFilter degrades to all and reports a warning", () => {
  const { state, warnings } = applyViewState(
    savedView({
      statusFilter: "crashlooping" as SavedViewConfig["statusFilter"],
    }),
  );
  assertEquals(state.statusFilter, "all");
  assertEquals(warnings.length, 1);
  assertStringIncludes(warnings[0], "status filter");
  assertStringIncludes(warnings[0], "crashlooping");
});

Deno.test("applyViewState: unsupported sortDir degrades to asc and reports a warning", () => {
  const { state, warnings } = applyViewState(
    savedView({ sortDir: "sideways" as SavedViewConfig["sortDir"] }),
  );
  assertEquals(state.sortDir, "asc");
  assertEquals(warnings.length, 1);
  assertStringIncludes(warnings[0], "sort direction");
  assertStringIncludes(warnings[0], "sideways");
});

Deno.test("applyViewState: unknown schemaVersion is reported, not silently applied", () => {
  const { state, warnings } = applyViewState(savedView({ schemaVersion: 99 }));
  // The readable fields still restore — the record is opened on a best-effort
  // basis — but the drift is named rather than swallowed.
  assertEquals(state.sortKey, "age");
  assertEquals(warnings.length, 1);
  assertStringIncludes(warnings[0], "schema version");
  assertStringIncludes(warnings[0], "99");
});

Deno.test("pinDedupKey: identical for two UIDs of the same kind/ns/name", () => {
  const original = pin({ uid: "aaaa-1111" });
  const recreated = pin({ uid: "bbbb-2222" });
  assertEquals(pinDedupKey(original), pinDedupKey(recreated));
  assertEquals(pinDedupKey(original), "deployments/default/api");
});

Deno.test("pinDedupKey: a cluster-scoped pin keeps an empty namespace segment", () => {
  // The server derives the same key from kind/namespace/name, and a
  // cluster-scoped kind carries no namespace. The empty segment has to stay:
  // collapsing "nodes//node-1" to "nodes/node-1" would put a cluster-scoped
  // pin in the same key space as a namespaced one called "node-1" in
  // namespace "nodes".
  const clusterScoped = pin({
    resourceKind: "nodes",
    namespace: "",
    name: "node-1",
  });
  assertEquals(pinDedupKey(clusterScoped), "nodes//node-1");
});

Deno.test("classifyPin: an error outcome is unknown, never ok or missing", () => {
  // The lookup failed, so nothing is known about the target. Reporting
  // "missing" would claim the object was deleted; reporting "ok" would claim
  // it was verified. Both are assertions the client cannot support when the
  // backend is unreachable (R3).
  const stored = pin();
  assertEquals(classifyPin(stored, { status: "error" }), "unknown");
  assertEquals(
    classifyPin(stored, { status: "error", liveUid: stored.uid }),
    "unknown",
  );
});

Deno.test("classifyPin: same uid returns ok", () => {
  const stored = pin();
  assertEquals(
    classifyPin(stored, { status: "ok", liveUid: stored.uid }),
    "ok",
  );
});

Deno.test("classifyPin: different uid returns replaced", () => {
  assertEquals(
    classifyPin(pin(), {
      status: "ok",
      liveUid: "99999999-0000-0000-0000-000000000000",
    }),
    "replaced",
  );
});

Deno.test("classifyPin: notFound returns missing", () => {
  assertEquals(classifyPin(pin(), { status: "notFound" }), "missing");
});

Deno.test("classifyPin: forbidden returns forbidden, never missing", () => {
  assertEquals(classifyPin(pin(), { status: "forbidden" }), "forbidden");
});

Deno.test("classifyPin: empty stored uid never returns ok", () => {
  assertEquals(
    classifyPin(pin({ uid: "" }), { status: "ok", liveUid: "aaaa-1111" }),
    "unknown",
  );
});

Deno.test("classifyPin: a live lookup with no uid is unknown, not ok", () => {
  assertEquals(classifyPin(pin(), { status: "ok" }), "unknown");
});

Deno.test("classifyPin: a failed lookup is unknown, not missing", () => {
  assertEquals(classifyPin(pin(), { status: "error" }), "unknown");
});

Deno.test("classifyPin: an unsupported pin schema is unknown, never ok", () => {
  const stored = pin({ schemaVersion: 2 });
  assertEquals(
    classifyPin(stored, { status: "ok", liveUid: stored.uid }),
    "unknown",
  );
});

Deno.test("classifyPin: an unsupported schema still reports missing and forbidden", () => {
  // These two outcomes do not depend on reading the stored envelope, so the
  // schema gate must not flatten them into "unknown" -- a deleted target and a
  // revoked permission are still those facts whatever schema wrote the record.
  const stored = pin({ schemaVersion: 2 });
  assertEquals(classifyPin(stored, { status: "notFound" }), "missing");
  assertEquals(classifyPin(stored, { status: "forbidden" }), "forbidden");
});

Deno.test("isSupportedSavedViewSchema / isSupportedPinSchema gate on the current version", () => {
  assertEquals(isSupportedSavedViewSchema(1), true);
  assertEquals(isSupportedSavedViewSchema(0), false);
  assertEquals(isSupportedSavedViewSchema(2), false);
  assertEquals(isSupportedPinSchema(1), true);
  assertEquals(isSupportedPinSchema(2), false);
});

// Parity with the Go contract. Each assertion below is pinned to the same
// literal as its counterpart in backend/internal/preferences/parity_test.go
// (TestContractParity), so a change on either side fails on that side and the
// message names the file holding the second edit. Comparing these constants to
// each other would prove nothing: both copies live in this repo, and the one
// that matters is the server's.
Deno.test("SAVED_VIEW_STATUS_FILTERS matches the Go allowedStatusFilters", () => {
  assertEquals(
    [...SAVED_VIEW_STATUS_FILTERS].sort(),
    ["all", "failed", "pending", "progressing", "running"],
    "drifted from backend/internal/preferences/types.go allowedStatusFilters",
  );
});

Deno.test("SAVED_VIEW_SORT_KEYS matches the Go allowedSortKeys", () => {
  assertEquals(
    [...SAVED_VIEW_SORT_KEYS].sort(),
    ["age", "name", "namespace"],
    "drifted from backend/internal/preferences/types.go allowedSortKeys",
  );
});

Deno.test("SAVED_VIEW_SORT_DIRS matches the Go allowedSortDirs", () => {
  assertEquals(
    [...SAVED_VIEW_SORT_DIRS].sort(),
    ["asc", "desc"],
    "drifted from backend/internal/preferences/types.go allowedSortDirs",
  );
});

Deno.test("schema versions, ceilings and bounds match their Go constants", () => {
  assertEquals(SAVED_VIEW_SCHEMA_VERSION, 1, "Go SavedViewSchemaVersion");
  assertEquals(PIN_SCHEMA_VERSION, 1, "Go PinSchemaVersion");
  assertEquals(MAX_SAVED_VIEWS, 100, "Go MaxSavedViewsPerUser");
  assertEquals(MAX_PINS, 200, "Go MaxPinsPerUser");
  assertEquals(MAX_RECORD_NAME_LEN, 128, "Go maxRecordNameLen");
  assertEquals(MAX_SEARCH_LEN, 256, "Go maxSearchLen");
});

Deno.test("SAVED_VIEW_SORT_KEYS is what ResourceTable marks sortable", () => {
  // islands/ResourceTable.tsx now derives its sortable-column gate from this
  // same constant, so the two cannot disagree. This test pins the value that
  // both the table and the Go allowlist are held to; widening it means
  // widening the comparator in that island and allowedSortKeys in Go.
  assertEquals([...SAVED_VIEW_SORT_KEYS], ["name", "namespace", "age"]);
});
