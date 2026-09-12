import { assertEquals } from "jsr:@std/assert@1";
import {
  applyViewState,
  captureViewState,
  classifyPin,
  isSupportedPinSchema,
  isSupportedSavedViewSchema,
  type PinConfig,
  pinDedupKey,
  SAVED_VIEW_SCHEMA_VERSION,
  SAVED_VIEW_SORT_KEYS,
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
});

Deno.test("applyViewState: unknown statusFilter degrades to all and reports a warning", () => {
  const { state, warnings } = applyViewState(
    savedView({
      statusFilter: "crashlooping" as SavedViewConfig["statusFilter"],
    }),
  );
  assertEquals(state.statusFilter, "all");
  assertEquals(warnings.length, 1);
});

Deno.test("applyViewState: unsupported sortDir degrades to asc and reports a warning", () => {
  const { state, warnings } = applyViewState(
    savedView({ sortDir: "sideways" as SavedViewConfig["sortDir"] }),
  );
  assertEquals(state.sortDir, "asc");
  assertEquals(warnings.length, 1);
});

Deno.test("applyViewState: unknown schemaVersion is reported, not silently applied", () => {
  const { state, warnings } = applyViewState(savedView({ schemaVersion: 99 }));
  // The readable fields still restore — the record is opened on a best-effort
  // basis — but the drift is named rather than swallowed.
  assertEquals(state.sortKey, "age");
  assertEquals(warnings.length, 1);
});

Deno.test("pinDedupKey: identical for two UIDs of the same kind/ns/name", () => {
  const original = pin({ uid: "aaaa-1111" });
  const recreated = pin({ uid: "bbbb-2222" });
  assertEquals(pinDedupKey(original), pinDedupKey(recreated));
  assertEquals(pinDedupKey(original), "deployments/default/api");
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

Deno.test("isSupportedSavedViewSchema / isSupportedPinSchema gate on the current version", () => {
  assertEquals(isSupportedSavedViewSchema(1), true);
  assertEquals(isSupportedSavedViewSchema(0), false);
  assertEquals(isSupportedSavedViewSchema(2), false);
  assertEquals(isSupportedPinSchema(1), true);
  assertEquals(isSupportedPinSchema(2), false);
});

Deno.test("SAVED_VIEW_SORT_KEYS matches the ResourceTable comparator", () => {
  // islands/ResourceTable.tsx sorts exactly these three keys and falls back to
  // "name" for anything else. If this assertion fails because a key was added
  // here, widen that comparator first — otherwise a saved view restores a sort
  // the table does not honour. If it fails because a key was removed, the
  // backend allowlist in internal/preferences/types.go needs the same edit.
  assertEquals([...SAVED_VIEW_SORT_KEYS], ["name", "namespace", "age"]);
});
