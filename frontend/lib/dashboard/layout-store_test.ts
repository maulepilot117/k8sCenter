import { expect, test } from "bun:test";
import { DEFAULT_OVERVIEW_LAYOUT } from "./default-layout.ts";
import {
  dropUnknownWidgets,
  layout,
  layoutFromResponse,
  layoutRevision,
  layoutWithheld,
  saveLayout,
  WithheldLayoutError,
} from "./layout-store.ts";
import { registerWidget } from "./registry.ts";
import type { DashboardLayoutConfig, LayoutItem, WidgetDef } from "./types.ts";
import { DASHBOARD_COLUMNS, DASHBOARD_LAYOUT_SCHEMA_VERSION } from "./types.ts";

// The registry is append-only and `bun test` shares module state across test
// files in one run, so these ids carry the `fixture-` prefix registry_test.ts
// filters on. Without it the catalog drift guard there would see two widgets
// that are not in the shipped catalog and fail whichever file ran second.
const KNOWN = "fixture-layout-store-known";
const ALSO_KNOWN = "fixture-layout-store-also-known";

function defFixture(id: string): WidgetDef {
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
  };
}

registerWidget(defFixture(KNOWN));
registerWidget(defFixture(ALSO_KNOWN));

function item(instanceId: string, id: string): LayoutItem {
  return { instanceId, id, x: 0, y: 0, w: 4, h: 4 };
}

function config(items: LayoutItem[]): DashboardLayoutConfig {
  return {
    schemaVersion: DASHBOARD_LAYOUT_SCHEMA_VERSION,
    scope: "overview",
    columns: DASHBOARD_COLUMNS,
    items,
  };
}

test("dropUnknownWidgets: keeps known ids untouched", () => {
  const input = config([item("a", KNOWN), item("b", ALSO_KNOWN)]);
  const { config: kept, warnings } = dropUnknownWidgets(input);

  expect(kept.items).toEqual(input.items);
  expect(warnings).toEqual([]);
  // The envelope travels with the items: a caller rendering `kept` must not
  // have to reach back to the input for the column count it lays out against.
  expect(kept.schemaVersion).toBe(input.schemaVersion);
  expect(kept.scope).toBe(input.scope);
  expect(kept.columns).toBe(input.columns);
});

test("dropUnknownWidgets: drops an unknown id and names it", () => {
  const { config: kept, warnings } = dropUnknownWidgets(
    config([item("a", KNOWN), item("b", "widget-from-the-future")]),
  );

  expect(kept.items.map((i) => i.instanceId)).toEqual(["a"]);
  expect(warnings).toHaveLength(1);
  // The user is told which widget vanished. "Something was removed" is worse
  // than saying nothing: it reports a loss the user cannot act on.
  expect(warnings[0]).toContain("widget-from-the-future");
});

test("dropUnknownWidgets: names every unknown id, not just the first", () => {
  const { config: kept, warnings } = dropUnknownWidgets(
    config([item("a", "gone-one"), item("b", KNOWN), item("c", "gone-two")]),
  );

  expect(kept.items.map((i) => i.instanceId)).toEqual(["b"]);
  expect(warnings).toHaveLength(2);
  expect(warnings.join(" ")).toContain("gone-one");
  expect(warnings.join(" ")).toContain("gone-two");
});

test("dropUnknownWidgets: dropping every widget yields an empty layout, not the default", () => {
  // Falling back to the shipped default here would silently discard a layout
  // the user spent time on because one build was missing its widgets — and
  // the next save would write that default over their arrangement.
  const { config: kept, warnings } = dropUnknownWidgets(
    config([item("a", "gone-one"), item("b", "gone-two")]),
  );

  expect(kept.items).toEqual([]);
  expect(kept.items).not.toEqual(DEFAULT_OVERVIEW_LAYOUT.items);
  expect(warnings).toHaveLength(2);
});

test("dropUnknownWidgets: does not mutate its input", () => {
  const input = config([item("a", KNOWN), item("b", "gone-one")]);
  dropUnknownWidgets(input);

  expect(input.items).toHaveLength(2);
});

test("layoutFromResponse: no stored layout means the scope default at revision 0", () => {
  const loaded = layoutFromResponse(null, "overview");

  // `toBe`, not `toEqual`: loadLayout decides whether to re-mount the grid by
  // comparing this against what the store already holds, so the default has to
  // come back as the same object rather than a copy of it. Clone it here and
  // every unsaved dashboard re-mounts its grid a moment after first paint.
  expect(loaded.config).toBe(DEFAULT_OVERVIEW_LAYOUT);
  // 0 is the claim "I believe none exists", which is what the first save has
  // to send. Any other value would make that save a 409 against a layout the
  // server has no record of.
  expect(loaded.revision).toBe(0);
  expect(loaded.withheld).toEqual([]);
  expect(loaded.warnings).toEqual([]);
});

test("layoutFromResponse: a stored layout carries its revision and drops unknown ids", () => {
  const loaded = layoutFromResponse(
    {
      id: "p1",
      kind: "dashboard_layout",
      name: "overview",
      clusterId: "local",
      schemaVersion: DASHBOARD_LAYOUT_SCHEMA_VERSION,
      revision: 7,
      config: config([item("a", KNOWN), item("b", "widget-from-the-future")]),
      createdAt: "2026-09-19T00:00:00Z",
      updatedAt: "2026-09-19T00:00:00Z",
    },
    "overview",
  );

  expect(loaded.config.items.map((i) => i.instanceId)).toEqual(["a"]);
  expect(loaded.revision).toBe(7);
  expect(loaded.warnings).toHaveLength(1);
});

test("layoutFromResponse: an omitted withheld key reads as an empty list", () => {
  // `withheld` is `omitempty` on the Go side, so an unfiltered response has no
  // such key at all. Passing that `undefined` through would make every
  // ordinary load throw at the first `.length`.
  const loaded = layoutFromResponse(
    {
      id: "p1",
      kind: "dashboard_layout",
      name: "overview",
      clusterId: "local",
      schemaVersion: DASHBOARD_LAYOUT_SCHEMA_VERSION,
      revision: 1,
      config: config([item("a", KNOWN)]),
      createdAt: "2026-09-19T00:00:00Z",
      updatedAt: "2026-09-19T00:00:00Z",
    },
    "overview",
  );

  expect(loaded.withheld).toEqual([]);
});

test("layoutFromResponse: withheld instanceIds survive to the caller", () => {
  const loaded = layoutFromResponse(
    {
      id: "p1",
      kind: "dashboard_layout",
      name: "overview",
      clusterId: "local",
      schemaVersion: DASHBOARD_LAYOUT_SCHEMA_VERSION,
      revision: 3,
      config: config([item("a", KNOWN)]),
      createdAt: "2026-09-19T00:00:00Z",
      updatedAt: "2026-09-19T00:00:00Z",
      withheld: ["b", "c"],
    },
    "overview",
  );

  expect(loaded.withheld).toEqual(["b", "c"]);
  // The withheld placements are NOT in the config — the server already removed
  // them. That is exactly why the list has to arrive separately.
  expect(loaded.config.items.map((i) => i.instanceId)).toEqual(["a"]);
});

test("saveLayout: refuses to write back a layout the server filtered", async () => {
  const before = { layout: layout.value, revision: layoutRevision.value };
  layoutWithheld.value = ["b"];

  try {
    // No transport is reachable here and none is needed: the refusal happens
    // before the request. That is the point — the stored layout still holds
    // the withheld placements, and a PUT carrying the filtered config would
    // pass the revision check and delete them permanently.
    let threw: unknown;
    await saveLayout("overview", config([item("a", KNOWN)])).catch((e) => {
      threw = e;
    });
    expect(threw).toBeInstanceOf(WithheldLayoutError);
    // The working copy is untouched: a refused save must not look like an
    // applied one.
    expect(layout.value).toBe(before.layout);
    expect(layoutRevision.value).toBe(before.revision);
  } finally {
    layoutWithheld.value = [];
    layout.value = before.layout;
    layoutRevision.value = before.revision;
  }
});

test("saveLayout: the refusal names what would have been lost", async () => {
  layoutWithheld.value = ["d-prod-diagnostics"];

  try {
    await saveLayout("overview", config([item("a", KNOWN)]));
    throw new Error("expected saveLayout to reject");
  } catch (err) {
    expect(err).toBeInstanceOf(WithheldLayoutError);
    expect((err as WithheldLayoutError).withheld).toEqual([
      "d-prod-diagnostics",
    ]);
    expect((err as Error).message).toContain("d-prod-diagnostics");
  } finally {
    layoutWithheld.value = [];
  }
});
