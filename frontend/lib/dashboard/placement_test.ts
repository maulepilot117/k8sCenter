import { describe, expect, test } from "bun:test";
import { overlaps } from "./grid.ts";
import { newInstanceId, placeNewWidget } from "./placement.ts";
import type { LayoutItem, WidgetDef } from "./types.ts";
import { DASHBOARD_COLUMNS } from "./types.ts";

// Where a widget added from the palette lands. The palette itself is a
// component and therefore untested by construction (D-10); this is the part of
// "Add widget" that can be wrong in a way the user notices -- a widget dropped
// on top of another, or one parked below the fold where the click appears to
// have done nothing.

function def(
  id: string,
  defaultW: number,
  defaultH: number,
  over: Partial<WidgetDef> = {},
): WidgetDef {
  return {
    id,
    title: id,
    family: "cluster",
    scopes: ["overview"],
    sources: ["dashboard-summary"],
    minW: 2,
    minH: 2,
    defaultW,
    defaultH,
    modes: ["normal"],
    render: () => null as unknown as ReturnType<WidgetDef["render"]>,
    ...over,
  };
}

function item(
  instanceId: string,
  x: number,
  y: number,
  w: number,
  h: number,
): LayoutItem {
  return { instanceId, id: `w-${instanceId}`, x, y, w, h };
}

/** Every existing item the placement lands on top of, named. */
function collisions(
  placed: LayoutItem,
  items: readonly LayoutItem[],
): string[] {
  return items.filter((i) => overlaps(placed, i)).map((i) => i.instanceId);
}

describe("placeNewWidget", () => {
  test("the first widget goes to the origin", () => {
    const out = placeNewWidget([], def("cluster-health", 4, 4));
    expect({ x: out.x, y: out.y }).toEqual({ x: 0, y: 0 });
  });

  test("uses the widget's default size", () => {
    const out = placeNewWidget([], def("nodes", 5, 3));
    expect({ w: out.w, h: out.h }).toEqual({ w: 5, h: 3 });
  });

  test("fills a gap in an existing row before starting a new one", () => {
    // A new widget dropped at the very bottom of a long dashboard is
    // invisible; the user clicks Add and appears to get nothing. Columns 8-11
    // of the top row are free and the widget fits there.
    const items = [item("a", 0, 0, 8, 4), item("b", 0, 4, 12, 4)];
    const out = placeNewWidget(items, def("new", 4, 4));
    expect({ x: out.x, y: out.y }).toEqual({ x: 8, y: 0 });
  });

  test("falls to a new row when no gap is wide enough", () => {
    // The same free columns, but this widget needs five of them.
    const items = [item("a", 0, 0, 8, 4), item("b", 0, 4, 12, 4)];
    const out = placeNewWidget(items, def("new", 5, 3));
    expect({ x: out.x, y: out.y }).toEqual({ x: 0, y: 8 });
  });

  test("a gap shorter than the widget is not a gap", () => {
    // Columns 8-11 are free for two rows only: placing a four-row widget
    // there would overlap `c` two rows down.
    const items = [
      item("a", 0, 0, 8, 4),
      item("c", 8, 2, 4, 2),
      item("b", 0, 4, 12, 4),
    ];
    const out = placeNewWidget(items, def("new", 4, 4));
    expect({ x: out.x, y: out.y }).toEqual({ x: 0, y: 8 });
  });

  test("never overlaps and never exceeds the grid width", () => {
    // A ragged layout with holes of several shapes, filled one widget at a
    // time: every placement has to be legal against everything already there,
    // not just against the layout it started from.
    let items: LayoutItem[] = [
      item("a", 0, 0, 3, 2),
      item("b", 5, 0, 4, 5),
      item("c", 0, 3, 2, 6),
      item("d", 9, 1, 3, 3),
    ];
    for (const [i, size] of [
      [2, 2],
      [4, 3],
      [6, 2],
      [3, 5],
      [12, 1],
    ].entries()) {
      const out = placeNewWidget(items, def(`new-${i}`, size[0], size[1]));
      expect(collisions(out, items)).toEqual([]);
      expect(out.x).toBeGreaterThanOrEqual(0);
      expect(out.x + out.w).toBeLessThanOrEqual(DASHBOARD_COLUMNS);
      expect(out.y).toBeGreaterThanOrEqual(0);
      items = [...items, out];
    }
  });

  test("a widget wider than the grid is clamped, not dropped", () => {
    const out = placeNewWidget([], def("wide", 16, 3), 12);
    expect(out.w).toBe(12);
    expect(out.x).toBe(0);
  });

  test("honours a narrower grid", () => {
    // `columns` is the layout's, not the constant: a stored layout carries its
    // own column count and a placement wider than that one is off the grid.
    const out = placeNewWidget([item("a", 0, 0, 4, 2)], def("new", 4, 2), 6);
    expect(out.x + out.w).toBeLessThanOrEqual(6);
    expect({ x: out.x, y: out.y }).toEqual({ x: 0, y: 2 });
  });

  test("a default size below the widget's own minimum is raised to it", () => {
    // Both come from the same registration, so disagreeing is a catalog bug --
    // but the editor refuses to resize below minW/minH and the server refuses
    // to store a placement under them, so emitting one would produce a widget
    // that cannot be saved and cannot be shrunk to explain why.
    const out = placeNewWidget([], def("odd", 1, 1, { minW: 3, minH: 2 }));
    expect({ w: out.w, h: out.h }).toEqual({ w: 3, h: 2 });
  });

  test("carries the widget id and a fresh instance id", () => {
    const d = def("cluster-health", 4, 4);
    const first = placeNewWidget([], d);
    const second = placeNewWidget([first], d);
    expect(first.id).toBe("cluster-health");
    expect(first.instanceId).not.toBe(second.instanceId);
  });

  test("does not mutate the layout it was given", () => {
    const items = [item("a", 0, 0, 4, 4)];
    placeNewWidget(items, def("new", 4, 4));
    expect(items).toHaveLength(1);
    expect(items[0]).toEqual(item("a", 0, 0, 4, 4));
  });
});

describe("newInstanceId", () => {
  test("is prefixed with the widget id and is unique per call", () => {
    // Not the widget id, and not an index: an index is reused after a removal,
    // so a stale render could key a new widget to a removed one's state.
    const ids = new Set(
      Array.from({ length: 50 }, () => newInstanceId("cluster-health")),
    );
    expect(ids.size).toBe(50);
    for (const id of ids) expect(id.startsWith("cluster-health-")).toBe(true);
  });

  test("stays inside the stored length bound", () => {
    // `maxInstanceIDLen` in backend/internal/preferences/dashboard.go is 64
    // runes and a save carrying a longer one is refused. A generated id must
    // not be the thing that makes a layout unsaveable, and the suffix is what
    // this side controls.
    expect(
      newInstanceId("external-secrets-sync-history").length,
    ).toBeLessThanOrEqual(64);
  });
});
