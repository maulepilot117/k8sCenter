import { expect, test } from "bun:test";
import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
// Importing the widget modules is what registers them, and registration is
// what gives the invariants below something to check. Without registration
// every invariant iterates an empty registry and passes vacuously, so a
// malformed widget would ship green.
//
// The manifest is the single import list, shared with the production render
// path so the two cannot register different sets. The drift guard at the
// bottom of this file checks it against the directory, so adding a widget
// file without listing it fails here rather than silently shrinking what the
// invariants cover.
import "@/components/dashboard/widgets/index.ts";
import { DEFAULT_OVERVIEW_LAYOUT } from "./default-layout.ts";
import { KNOWN_PARAM_KEYS } from "./params.ts";
import {
  allWidgets,
  getWidget,
  isRetiredWidgetId,
  RETIRED_WIDGET_IDS,
  registerWidget,
  widgetsForScope,
} from "./registry.ts";
import type { WidgetDef } from "./types.ts";
import {
  DASHBOARD_SCOPES,
  DATA_SOURCE_KEYS,
  DISPLAY_MODES,
  WIDGET_FAMILIES,
} from "./types.ts";

// The registry is the allowlist the server validates against and the catalog
// the palette renders. Every invariant below exists because breaking it
// produces a layout that cannot be rendered, stored, or restored.
//
// Most invariants iterate the registered set, which the manifest import above
// populates with every shipped widget. Each one collects offenders rather than
// asserting per widget, so a failure names the widget instead of only the
// assertion that tripped.
//
// READ BEFORE ADDING A COUNT ASSERTION: `bun test` shares module state across
// test files in one run (verified, not assumed), and the registry is
// append-only by design -- there is no unregister. So the `fixture-*` widgets
// registered below stay in the registry for every other test file in the same
// run. `expect(allWidgets()).toHaveLength(10)` in a D4/D5 test would therefore
// see 12 and fail. Assert on ids, or filter out the `fixture-` prefix.
// The fixtures are deliberately valid definitions so they cannot trip the
// invariants above regardless of the order test files happen to run in.

const KEBAB = /^[a-z0-9]+(-[a-z0-9]+)*$/;

/** Minimal valid definition, for the tests that exercise registration. */
function defFixture(id: string, over: Partial<WidgetDef> = {}): WidgetDef {
  return {
    id,
    title: "Fixture",
    family: "cluster",
    scopes: ["overview"],
    sources: ["dashboard-summary"],
    minW: 2,
    minH: 2,
    defaultW: 4,
    defaultH: 4,
    modes: ["normal"],
    render: () => null as unknown as ReturnType<WidgetDef["render"]>,
    ...over,
  };
}

test("registry: ids are unique", () => {
  const ids = allWidgets().map((w) => w.id);
  expect(ids.length).toBe(new Set(ids).size);
});

test("registry: ids are kebab-case", () => {
  const offenders = allWidgets()
    .map((w) => w.id)
    .filter((id) => !KEBAB.test(id));
  expect(offenders).toEqual([]);
});

test("registry: no live widget reuses a retired id", () => {
  // A reused id would silently resurrect a stored placement that meant
  // something else.
  const offenders = allWidgets()
    .map((w) => w.id)
    .filter((id) => isRetiredWidgetId(id));
  expect(offenders).toEqual([]);
});

test("registry: every widget implements normal", () => {
  // pickMode falls back toward normal from both directions. A widget without
  // it has no guaranteed rendering at any size.
  const offenders = allWidgets()
    .filter((w) => !w.modes.includes("normal"))
    .map((w) => w.id);
  expect(offenders).toEqual([]);
});

test("registry: modes, family, scopes and sources are all declared values", () => {
  const offenders: string[] = [];
  for (const w of allWidgets()) {
    for (const m of w.modes) {
      if (!DISPLAY_MODES.includes(m)) offenders.push(`${w.id} mode ${m}`);
    }
    if (!WIDGET_FAMILIES.includes(w.family)) {
      offenders.push(`${w.id} family ${w.family}`);
    }
    if (w.scopes.length === 0) offenders.push(`${w.id} has no scope`);
    for (const s of w.scopes) {
      if (!DASHBOARD_SCOPES.includes(s)) offenders.push(`${w.id} scope ${s}`);
    }
    for (const src of w.sources) {
      if (!DATA_SOURCE_KEYS.includes(src)) {
        offenders.push(`${w.id} source ${src}`);
      }
    }
  }
  expect(offenders).toEqual([]);
});

test("registry: default size is at least the minimum size", () => {
  const offenders: string[] = [];
  for (const w of allWidgets()) {
    if (w.defaultW < w.minW) offenders.push(`${w.id} defaultW < minW`);
    if (w.defaultH < w.minH) offenders.push(`${w.id} defaultH < minH`);
  }
  expect(offenders).toEqual([]);
});

test("registry: nothing is wider than the grid", () => {
  const offenders: string[] = [];
  for (const w of allWidgets()) {
    if (w.defaultW > 12) offenders.push(`${w.id} defaultW exceeds 12 columns`);
    if (w.minW < 1) offenders.push(`${w.id} minW below 1`);
  }
  expect(offenders).toEqual([]);
});

test("registry: nothing is short enough to stack the editor's two handles", () => {
  // A row is DASHBOARD_ROW_HEIGHT tall, so minH 1 is a 40px card -- exactly the
  // height of the drag handle that covers the title row. The 24px resize grip
  // in the bottom-right corner would then sit inside the drag handle's box,
  // stealing its last 24px and putting two pointer targets on top of each
  // other, which WCAG 2.2 AA Target Size (Minimum) forbids of adjacent targets.
  //
  // minH 2 is a 96px card, which leaves the two 32px apart. This is the
  // invariant DashboardGrid.tsx's handle geometry depends on and the types
  // cannot express.
  const offenders = allWidgets()
    .filter((w) => w.minH < 2)
    .map((w) => `${w.id} minH below 2`);
  expect(offenders).toEqual([]);
});

test("getWidget: unknown id is undefined, not a throw", () => {
  // Reads drop unknown ids with a notice (spec D-7), so lookup must be
  // total rather than exceptional.
  expect(getWidget("no-such-widget")).toBeUndefined();
});

test("widgetsForScope: returns only widgets declaring that scope", () => {
  const offenders = widgetsForScope("overview")
    .filter((w) => !w.scopes.includes("overview"))
    .map((w) => w.id);
  expect(offenders).toEqual([]);
});

test("registerWidget: a widget becomes findable by id and scope", () => {
  const id = "fixture-registers";
  registerWidget(defFixture(id));
  expect(getWidget(id)?.id).toBe(id);
  expect(widgetsForScope("overview").map((w) => w.id)).toContain(id);
});

test("registerWidget: a duplicate id throws", () => {
  // Second registration of an id would otherwise silently replace the first,
  // surfacing as a widget that renders the wrong thing.
  const id = "fixture-duplicate";
  registerWidget(defFixture(id));
  expect(() => registerWidget(defFixture(id))).toThrow(
    `duplicate widget id ${id}`,
  );
});

test("registerWidget: a definition without normal is rejected", () => {
  // Enforced at the runtime boundary, not only by the invariant test above.
  // pickMode returns "normal" for an empty mode list (undefined would be
  // worse), so a widget lacking it would receive a mode it does not implement.
  expect(() =>
    registerWidget(defFixture("fixture-no-normal", { modes: ["compact"] })),
  ).toThrow('must implement the "normal" display mode');
  expect(() =>
    registerWidget(defFixture("fixture-empty-modes", { modes: [] })),
  ).toThrow('must implement the "normal" display mode');
  // Rejected means not registered, not registered-then-flagged.
  expect(getWidget("fixture-no-normal")).toBeUndefined();
  expect(getWidget("fixture-empty-modes")).toBeUndefined();
});

test("RETIRED_WIDGET_IDS cannot be mutated at runtime", () => {
  // Retirement is permanent. Note this is a frozen array, not a frozen Set:
  // Object.freeze does not prevent Set.prototype.add, so a Set here would
  // document a guarantee it does not have.
  expect(() => {
    (RETIRED_WIDGET_IDS as string[]).push("cluster-health");
  }).toThrow();
  expect(RETIRED_WIDGET_IDS).toHaveLength(0);
});

test("isRetiredWidgetId: a live id is not retired", () => {
  expect(isRetiredWidgetId("cluster-health")).toBe(false);
});

test("optionalSources is always a subset of sources", () => {
  // An optional source that is not declared in `sources` is never fetched, so
  // the widget would render forever against a source nobody requested.
  const offenders: string[] = [];
  for (const w of allWidgets()) {
    for (const k of w.optionalSources ?? []) {
      if (!w.sources.includes(k)) {
        offenders.push(`${w.id} marks ${k} optional but does not declare it`);
      }
    }
  }
  expect(offenders).toEqual([]);
});

test("registry ids are pinned to the server-side allowlist", () => {
  // The other half of a cross-language contract. The server validates a saved
  // layout against `allowedWidgetIDs` in
  // backend/internal/preferences/dashboard.go, which is a Go map and cannot
  // read this registry -- so a widget added here and not there produces a
  // layout the user can build in the editor and the server then refuses on
  // save, with no test failing anywhere.
  //
  // Pinning both sides to the same literal turns that into a red test on
  // whichever side was forgotten. The Go half is TestContractParity in
  // backend/internal/preferences/parity_test.go; adding a widget means
  // editing three places, and forgetting any one of them fails here or there.
  //
  // `fixture-*` ids are filtered out: `bun test` shares module state across
  // files and the registry is append-only, so the registration tests above
  // leave their fixtures behind (see the note at the top of this file).
  const ids = allWidgets()
    .map((w) => w.id)
    .filter((id) => !id.startsWith("fixture-"))
    .sort();
  expect(ids).toEqual([
    "active-alerts",
    "cluster-health",
    "cpu-tile",
    "diagnostics-summary",
    "memory-tile",
    "network-tile",
    "nodes",
    "pod-status",
    "pods-tile",
    "recent-events",
    "resource-utilization",
  ]);
});

test("registry minimums are pinned to the server-side catalog", () => {
  // The other half of the size contract. The server refuses a placement below
  // a widget's declared minimum, using its own copy of these numbers in
  // `allowedWidgets` (backend/internal/preferences/dashboard.go) because it
  // cannot read this registry. A minimum changed here and not there produces
  // an editor that lets the user resize to something the server then rejects,
  // citing a bound the client never showed -- or the reverse, an editor that
  // refuses a size the server would have taken.
  //
  // The Go half is TestContractParity/"widget specs" in
  // backend/internal/preferences/parity_test.go, which pins the same pairs.
  const mins = Object.fromEntries(
    allWidgets()
      .filter((w) => !w.id.startsWith("fixture-"))
      .map((w) => [w.id, [w.minW, w.minH]]),
  );
  expect(mins).toEqual({
    "active-alerts": [2, 3],
    "cluster-health": [3, 4],
    "cpu-tile": [2, 2],
    "diagnostics-summary": [3, 3],
    "memory-tile": [2, 2],
    "network-tile": [2, 2],
    nodes: [3, 4],
    "pod-status": [3, 4],
    "pods-tile": [2, 2],
    "recent-events": [3, 3],
    "resource-utilization": [4, 4],
  });
});

test("the parameterized widgets and their declared keys are pinned", () => {
  // This replaced the "no shipped widget declares parameters yet" tripwire the
  // moment the first parameterized widget landed, which is exactly what that
  // tripwire was for. The pin is stricter than the one it replaced: the server
  // validates a stored value against the widget's own declaration
  // (`allowedWidgets` in backend/internal/preferences/dashboard.go), so a key
  // added here and not there makes every layout carrying it unsaveable, and a
  // value set narrowed on one side only makes the two disagree about which
  // values are legal.
  //
  // An empty array means "any value inside the generic bounds" -- a namespace
  // name, whose legal values are not knowable ahead of time -- and is
  // deliberately NOT the same as the key being absent. The Go half is
  // TestContractParity/"widget specs".
  const declared = Object.fromEntries(
    allWidgets()
      .filter((w) => !w.id.startsWith("fixture-"))
      .filter((w) => w.params !== undefined)
      .map((w) => [w.id, w.params]),
  );
  expect(declared).toEqual({
    "diagnostics-summary": { namespace: [] },
  });
});

test("every declared parameter key is one the server recognises", () => {
  // The namespace key is special-cased by the read path, which re-authorizes
  // its value on every read (R5). A widget spelling it differently would look
  // identical in the editor and in storage and would silently opt out of that
  // -- the placement would never be withheld, and a user who lost access to
  // the namespace would go on seeing the card. Nothing else would notice, so
  // this and `registerWidget`'s own guard are the whole defence.
  const offenders: string[] = [];
  for (const w of allWidgets()) {
    for (const key of Object.keys(w.params ?? {})) {
      if (!KNOWN_PARAM_KEYS.includes(key)) {
        offenders.push(`${w.id} declares unknown param ${key}`);
      }
    }
  }
  expect(offenders).toEqual([]);
});

test("registerWidget: an unknown parameter key is rejected", () => {
  // Enforced at the runtime boundary and not only by the invariant above: a
  // definition registered from anywhere has to satisfy it, and the failure
  // this prevents is invisible by construction.
  expect(() =>
    registerWidget(defFixture("fixture-bad-param", { params: { ns: [] } })),
  ).toThrow("ns");
  expect(getWidget("fixture-bad-param")).toBeUndefined();
});

test("the default layout satisfies every widget's declared minimum", () => {
  // The default is the one layout every user starts from, and it is now
  // validated server-side against these minimums -- so a minimum raised above
  // what the default uses would make the starting dashboard unsavable. The Go
  // side runs the same layout through the real validator; this catches the
  // mismatch at its source, where the numbers actually live.
  const offenders: string[] = [];
  for (const item of DEFAULT_OVERVIEW_LAYOUT.items) {
    const def = getWidget(item.id);
    if (!def) {
      offenders.push(`${item.id} is not registered`);
      continue;
    }
    if (item.w < def.minW) {
      offenders.push(`${item.id} w=${item.w} below minW=${def.minW}`);
    }
    if (item.h < def.minH) {
      offenders.push(`${item.id} h=${item.h} below minH=${def.minH}`);
    }
  }
  expect(offenders).toEqual([]);
});

test("every widget module is listed in the manifest", () => {
  // The drift guard. Registration is an import side effect, so a widget file
  // the manifest does not import registers nothing -- and every invariant
  // above would keep passing while covering one widget fewer than the
  // codebase contains. That is the vacuous-pass failure this suite exists to
  // prevent, narrowed to "widgets someone forgot to list".
  const dir = join(
    import.meta.dir,
    "..",
    "..",
    "components",
    "dashboard",
    "widgets",
  );
  const modules = readdirSync(dir).filter(
    (f) => f.endsWith(".tsx") && !f.endsWith("_test.tsx"),
  );
  const manifest = readFileSync(join(dir, "index.ts"), "utf8");

  expect(modules.length).toBeGreaterThan(0);
  const missing = modules.filter((f) => !manifest.includes(`"./${f}"`));
  expect(missing).toEqual([]);
});
