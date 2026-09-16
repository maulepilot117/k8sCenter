import { expect, test } from "bun:test";
// Importing the widget modules is what registers them, and registration is
// what gives the invariants below something to check. Without these imports
// every invariant iterates an empty registry and passes vacuously, so a
// malformed widget would ship green. Each new widget module belongs here.
import "@/components/dashboard/widgets/ClusterHealthWidget.tsx";
import "@/components/dashboard/widgets/CpuTileWidget.tsx";
import "@/components/dashboard/widgets/MemoryTileWidget.tsx";
import "@/components/dashboard/widgets/NetworkTileWidget.tsx";
import "@/components/dashboard/widgets/PodsTileWidget.tsx";
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
// Most invariants iterate the registered set, which is empty until D4/D5
// register the extracted widgets -- they pass vacuously today and gain teeth
// then. Each one collects offenders rather than asserting per widget, so a
// failure names the widget instead of only the assertion that tripped.
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
    if (w.minH < 1) offenders.push(`${w.id} minH below 1`);
  }
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
