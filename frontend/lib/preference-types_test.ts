import { expect, test } from "bun:test";
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

test("captureViewState: round-trips the four supported table signals", () => {
  const state = viewState();
  const { state: restored, warnings } = applyViewState(captureViewState(state));
  expect(restored).toEqual(state);
  expect(warnings).toEqual([]);
});

test('captureViewState: namespace "all" is stored as empty string', () => {
  // ResourceTable's `ns` computed already collapses the "all" sentinel to ""
  // before it reaches here, and "" is what the server stores for the
  // all-namespaces scope. This pins that the capture side does not reintroduce
  // the sentinel, which the server would reject as an invalid namespace.
  const captured = captureViewState(viewState({ namespace: "" }));
  expect(captured.namespace).toBe("");
  expect(applyViewState(captured).state.namespace).toBe("");
});

test("applyViewState: a cluster-scoped view restores an empty namespace", () => {
  // Mirrors the server's validateScope rule: a cluster-scoped kind must carry
  // no namespace. Restoring one would make the table request a namespaced URL
  // for a kind that has no namespaces.
  const stored = savedView({ resourceKind: "nodes", namespace: "" });
  const { state, warnings } = applyViewState(stored);
  expect(state.namespace).toBe("");
  expect(warnings).toEqual([]);
});

test("captureViewState: stamps the current schema version", () => {
  expect(
    captureViewState(viewState()).schemaVersion,
  ).toBe(SAVED_VIEW_SCHEMA_VERSION);
});

test("applyViewState: unsupported sortKey degrades to name and reports a warning", () => {
  const { state, warnings } = applyViewState(
    savedView({ sortKey: "cpu" as SavedViewConfig["sortKey"] }),
  );
  expect(state.sortKey).toBe("name");
  expect(warnings.length).toBe(1);
  // The message must name the offending value and the field it degraded.
  // Asserting only the count would pass on a warning about the wrong field.
  expect(warnings[0]).toContain("sort key");
  expect(warnings[0]).toContain("cpu");
});

test("applyViewState: unknown statusFilter degrades to all and reports a warning", () => {
  const { state, warnings } = applyViewState(
    savedView({
      statusFilter: "crashlooping" as SavedViewConfig["statusFilter"],
    }),
  );
  expect(state.statusFilter).toBe("all");
  expect(warnings.length).toBe(1);
  expect(warnings[0]).toContain("status filter");
  expect(warnings[0]).toContain("crashlooping");
});

test("applyViewState: unsupported sortDir degrades to asc and reports a warning", () => {
  const { state, warnings } = applyViewState(
    savedView({ sortDir: "sideways" as SavedViewConfig["sortDir"] }),
  );
  expect(state.sortDir).toBe("asc");
  expect(warnings.length).toBe(1);
  expect(warnings[0]).toContain("sort direction");
  expect(warnings[0]).toContain("sideways");
});

test("applyViewState: unknown schemaVersion is reported, not silently applied", () => {
  const { state, warnings } = applyViewState(savedView({ schemaVersion: 99 }));
  // The readable fields still restore — the record is opened on a best-effort
  // basis — but the drift is named rather than swallowed.
  expect(state.sortKey).toBe("age");
  expect(warnings.length).toBe(1);
  expect(warnings[0]).toContain("schema version");
  expect(warnings[0]).toContain("99");
});

test("pinDedupKey: identical for two UIDs of the same kind/ns/name", () => {
  const original = pin({ uid: "aaaa-1111" });
  const recreated = pin({ uid: "bbbb-2222" });
  expect(pinDedupKey(original)).toBe(pinDedupKey(recreated));
  expect(pinDedupKey(original)).toBe("deployments/default/api");
});

test("pinDedupKey: a cluster-scoped pin keeps an empty namespace segment", () => {
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
  expect(pinDedupKey(clusterScoped)).toBe("nodes//node-1");
});

test("classifyPin: an error outcome is unknown, never ok or missing", () => {
  // The lookup failed, so nothing is known about the target. Reporting
  // "missing" would claim the object was deleted; reporting "ok" would claim
  // it was verified. Both are assertions the client cannot support when the
  // backend is unreachable (R3).
  const stored = pin();
  expect(classifyPin(stored, { status: "error" })).toBe("unknown");
  expect(
    classifyPin(stored, { status: "error", liveUid: stored.uid }),
  ).toBe("unknown");
});

test("classifyPin: same uid returns ok", () => {
  const stored = pin();
  expect(
    classifyPin(stored, { status: "ok", liveUid: stored.uid }),
  ).toBe("ok");
});

test("classifyPin: different uid returns replaced", () => {
  expect(
    classifyPin(pin(), {
      status: "ok",
      liveUid: "99999999-0000-0000-0000-000000000000",
    }),
  ).toBe("replaced");
});

test("classifyPin: notFound returns missing", () => {
  expect(classifyPin(pin(), { status: "notFound" })).toBe("missing");
});

test("classifyPin: forbidden returns forbidden, never missing", () => {
  expect(classifyPin(pin(), { status: "forbidden" })).toBe("forbidden");
});

test("classifyPin: empty stored uid never returns ok", () => {
  expect(
    classifyPin(pin({ uid: "" }), { status: "ok", liveUid: "aaaa-1111" }),
  ).toBe("unknown");
});

test("classifyPin: a live lookup with no uid is unknown, not ok", () => {
  expect(classifyPin(pin(), { status: "ok" })).toBe("unknown");
});

test("classifyPin: a failed lookup is unknown, not missing", () => {
  expect(classifyPin(pin(), { status: "error" })).toBe("unknown");
});

test("classifyPin: an unsupported pin schema is unknown, never ok", () => {
  const stored = pin({ schemaVersion: 2 });
  expect(
    classifyPin(stored, { status: "ok", liveUid: stored.uid }),
  ).toBe("unknown");
});

test("classifyPin: an unsupported schema still reports missing and forbidden", () => {
  // These two outcomes do not depend on reading the stored envelope, so the
  // schema gate must not flatten them into "unknown" -- a deleted target and a
  // revoked permission are still those facts whatever schema wrote the record.
  const stored = pin({ schemaVersion: 2 });
  expect(classifyPin(stored, { status: "notFound" })).toBe("missing");
  expect(classifyPin(stored, { status: "forbidden" })).toBe("forbidden");
});

test("isSupportedSavedViewSchema / isSupportedPinSchema gate on the current version", () => {
  expect(isSupportedSavedViewSchema(1)).toBe(true);
  expect(isSupportedSavedViewSchema(0)).toBe(false);
  expect(isSupportedSavedViewSchema(2)).toBe(false);
  expect(isSupportedPinSchema(1)).toBe(true);
  expect(isSupportedPinSchema(2)).toBe(false);
});

// Parity with the Go contract. Each assertion below is pinned to the same
// literal as its counterpart in backend/internal/preferences/parity_test.go
// (TestContractParity), so a change on either side fails on that side and the
// message names the file holding the second edit. Comparing these constants to
// each other would prove nothing: both copies live in this repo, and the one
// that matters is the server's.
test("SAVED_VIEW_STATUS_FILTERS matches the Go allowedStatusFilters", () => {
  expect(
    [...SAVED_VIEW_STATUS_FILTERS].sort(),
  ).toEqual(
    ["all", "failed", "pending", "progressing", "running"],
  );
});

test("SAVED_VIEW_SORT_KEYS matches the Go allowedSortKeys", () => {
  expect(
    [...SAVED_VIEW_SORT_KEYS].sort(),
  ).toEqual(
    ["age", "name", "namespace"],
  );
});

test("SAVED_VIEW_SORT_DIRS matches the Go allowedSortDirs", () => {
  expect(
    [...SAVED_VIEW_SORT_DIRS].sort(),
  ).toEqual(
    ["asc", "desc"],
  );
});

test("schema versions, ceilings and bounds match their Go constants", () => {
  expect(SAVED_VIEW_SCHEMA_VERSION).toBe(1);
  expect(PIN_SCHEMA_VERSION).toBe(1);
  expect(MAX_SAVED_VIEWS).toBe(100);
  expect(MAX_PINS).toBe(200);
  expect(MAX_RECORD_NAME_LEN).toBe(128);
  expect(MAX_SEARCH_LEN).toBe(256);
});

test("SAVED_VIEW_SORT_KEYS is what ResourceTable marks sortable", () => {
  // islands/ResourceTable.tsx now derives its sortable-column gate from this
  // same constant, so the two cannot disagree. This test pins the value that
  // both the table and the Go allowlist are held to; widening it means
  // widening the comparator in that island and allowedSortKeys in Go.
  expect([...SAVED_VIEW_SORT_KEYS]).toEqual(["name", "namespace", "age"]);
});
