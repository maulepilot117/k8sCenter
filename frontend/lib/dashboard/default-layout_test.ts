import { expect, test } from "bun:test";
// Registers the shipped widgets. Same manifest the render path imports, so
// these checks see exactly the set the dashboard renders.
import "@/components/dashboard/widgets/index.ts";
import {
  DEFAULT_OVERVIEW_LAYOUT,
  defaultItem,
  type FlexCell,
  flexSlotIds,
  OVERVIEW_FLEX_ROWS,
} from "./default-layout.ts";
import { allWidgets, getWidget } from "./registry.ts";
import { DASHBOARD_COLUMNS, DASHBOARD_MAX_ITEMS } from "./types.ts";

// The default layout is what every new user sees and what "Reset" restores.
// It is data, so it can be wrong in ways a type cannot catch.

const items = DEFAULT_OVERVIEW_LAYOUT.items;

test("default layout: every id resolves to a registered widget", () => {
  const missing = items.filter((i) => getWidget(i.id) === undefined);
  expect(missing.map((i) => i.id)).toEqual([]);
});

test("default layout: contains every shipped widget exactly once", () => {
  // Compared by id, not by count: registry_test registers `fixture-*`
  // widgets, and bun shares module state across test files in one run.
  const shipped = allWidgets()
    .map((w) => w.id)
    .filter((id) => !id.startsWith("fixture-"))
    .sort();
  expect(items.map((i) => i.id).sort()).toEqual(shipped);
  expect(shipped).toHaveLength(10);
});

test("default layout: instance ids are unique", () => {
  const ids = items.map((i) => i.instanceId);
  expect(new Set(ids).size).toBe(ids.length);
});

test("default layout: declares the grid it is laid out on", () => {
  expect(DEFAULT_OVERVIEW_LAYOUT.columns).toBe(DASHBOARD_COLUMNS);
  expect(DEFAULT_OVERVIEW_LAYOUT.scope).toBe("overview");
});

test("default layout: nothing overflows the grid", () => {
  const bad = items.filter(
    (i) =>
      !Number.isInteger(i.x) ||
      !Number.isInteger(i.y) ||
      i.x < 0 ||
      i.y < 0 ||
      i.x + i.w > DASHBOARD_COLUMNS,
  );
  expect(bad.map((i) => i.id)).toEqual([]);
});

test("default layout: no two widgets overlap", () => {
  const overlaps: string[] = [];
  for (let a = 0; a < items.length; a++) {
    for (let b = a + 1; b < items.length; b++) {
      const p = items[a];
      const q = items[b];
      const disjoint =
        p.x + p.w <= q.x ||
        q.x + q.w <= p.x ||
        p.y + p.h <= q.y ||
        q.y + q.h <= p.y;
      if (!disjoint) overlaps.push(`${p.id}/${q.id}`);
    }
  }
  expect(overlaps).toEqual([]);
});

test("default layout: every widget is at least its declared minimum", () => {
  const small = items.filter((i) => {
    const def = getWidget(i.id);
    return def !== undefined && (i.w < def.minW || i.h < def.minH);
  });
  expect(small.map((i) => i.id)).toEqual([]);
});

test("default layout: every widget is available on its scope", () => {
  const offScope = items.filter(
    (i) => !getWidget(i.id)?.scopes.includes(DEFAULT_OVERVIEW_LAYOUT.scope),
  );
  expect(offScope.map((i) => i.id)).toEqual([]);
});

test("default layout: within the item cap", () => {
  expect(items.length).toBeLessThanOrEqual(DASHBOARD_MAX_ITEMS);
});

// The shell renders OVERVIEW_FLEX_ROWS, not the layout's coordinates. These
// hold the two together until P2 renders the layout directly: a widget in the
// layout with no flex slot would otherwise never appear, silently.

test("flex rows: render every default-layout widget exactly once", () => {
  const ids = flexSlotIds(OVERVIEW_FLEX_ROWS);
  expect(new Set(ids).size).toBe(ids.length);
  expect([...ids].sort()).toEqual(items.map((i) => i.id).sort());
});

test("flex rows: every slot has a skeleton height", () => {
  const cells = OVERVIEW_FLEX_ROWS.flat();
  const slots = cells.flatMap((c) => ("tiles" in c ? c.tiles : [c]));
  expect(slots.filter((s) => !s.placeholder).map((s) => s.id)).toEqual([]);
});

test("flexSlotIds: flattens tile blocks in render order", () => {
  const rows: FlexCell[][] = [
    [
      { id: "a", flex: "1", placeholder: "1px" },
      {
        flex: "1",
        tiles: [
          { id: "b", placeholder: "1px" },
          { id: "c", placeholder: "1px" },
        ],
      },
    ],
    [{ id: "d", flex: "1", placeholder: "1px" }],
  ];
  expect(flexSlotIds(rows)).toEqual(["a", "b", "c", "d"]);
});

test("defaultItem: returns the placement for a known id", () => {
  expect(defaultItem("nodes").instanceId).toBe("d-nodes");
});

test("defaultItem: throws for an id the layout does not contain", () => {
  expect(() => defaultItem("no-such-widget")).toThrow(/no-such-widget/);
});
