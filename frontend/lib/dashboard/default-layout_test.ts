import { expect, test } from "bun:test";
// Registers the shipped widgets. Same manifest the render path imports, so
// these checks see exactly the set the dashboard renders.
import "@/components/dashboard/widgets/index.ts";
import { DEFAULT_OVERVIEW_LAYOUT } from "./default-layout.ts";
import { byReadingOrder, compact } from "./grid.ts";
import { allWidgets, getWidget } from "./registry.ts";
import { DASHBOARD_COLUMNS, DASHBOARD_MAX_ITEMS } from "./types.ts";

// The default layout is what every new user sees and what "Reset" restores.
// It is data, so it can be wrong in ways a type cannot catch.

const items = DEFAULT_OVERVIEW_LAYOUT.items;

test("default layout: every id resolves to a registered widget", () => {
  const missing = items.filter((i) => getWidget(i.id) === undefined);
  expect(missing.map((i) => i.id)).toEqual([]);
});

test("default layout: is the curated starting set, pinned by id", () => {
  // This used to assert that the default held EVERY shipped parameterless
  // widget. That was true while the catalog was the ten cards the
  // pre-registry dashboard rendered and the default was simply those ten
  // re-expressed -- but the catalog is now an assembly kit, and a default
  // that grew with it would put every widget of every release on the
  // starting dashboard, run into DASHBOARD_MAX_ITEMS, and hand a new user a
  // wall of cards about features their cluster may not even run. The default
  // is a curated subset from here on, so it is pinned by literal instead:
  // adding a widget to the catalog no longer touches this list, and adding
  // one TO THE DEFAULT is a deliberate edit that has to say so here.
  expect(items.map((i) => i.id).sort()).toEqual([
    "active-alerts",
    "cluster-health",
    "cpu-tile",
    "memory-tile",
    "network-tile",
    "nodes",
    "pod-status",
    "pods-tile",
    "recent-events",
    "resource-utilization",
  ]);
});

test("default layout: holds only parameterless widgets", () => {
  // The half of the old assertion that still holds, kept because the reason
  // survives the catalog's growth: a widget that needs a value has no
  // defensible default. The layout every user starts from cannot know which
  // namespace any of them cares about, and picking one would put a card on
  // every dashboard that most accounts are not permitted to read and that
  // the server would withhold on load. A parameterized widget arrives when a
  // user adds it and chooses.
  const shipped = new Set(
    allWidgets()
      .filter((w) => w.params === undefined)
      .map((w) => w.id),
  );
  expect(items.filter((i) => !shipped.has(i.id)).map((i) => i.id)).toEqual([]);
});

test("default layout: carries no parameters", () => {
  // The other half of the rule above, stated on the data rather than on the
  // catalog: a default placement with a `params` field would be a namespace
  // chosen on the user's behalf, and the server refuses parameters outright
  // on a widget that declares none -- so a stray one here would make the
  // STARTING dashboard unsaveable.
  const parameterized = items.filter((i) => i.params !== undefined);
  expect(parameterized.map((i) => i.id)).toEqual([]);
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

// The grid applies gravity to every layout it renders. A default that is not
// already at rest would visibly jump on first paint and would not survive a
// save/load round trip unchanged.
test("default layout: is already compact", () => {
  expect(compact(items)).toEqual([...items].sort(byReadingOrder));
});

test("default layout: the four metric tiles form an equal 2x2 block", () => {
  const tiles = ["cpu-tile", "memory-tile", "pods-tile", "network-tile"].map(
    (id) => items.find((i) => i.id === id),
  );
  const sizes = new Set(tiles.map((t) => `${t?.w}x${t?.h}`));
  expect(sizes.size).toBe(1);
  const xs = new Set(tiles.map((t) => t?.x));
  const ys = new Set(tiles.map((t) => t?.y));
  expect([xs.size, ys.size]).toEqual([2, 2]);
});
