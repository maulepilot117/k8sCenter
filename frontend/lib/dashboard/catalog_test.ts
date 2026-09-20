import { describe, expect, test } from "bun:test";
import {
  ALREADY_PLACED,
  DASHBOARD_FULL,
  disabledReasonFor,
  NO_ROOM,
} from "./catalog.ts";
import type { LayoutItem, WidgetDef } from "./types.ts";
import {
  DASHBOARD_COLUMNS,
  DASHBOARD_MAX_ITEMS,
  DASHBOARD_MAX_ROWS,
} from "./types.ts";

// Why one catalog row is offered or refused. The palette itself is a
// component and therefore untested by construction (D-10); this is the part
// of "Add a widget" that decides what a user actually sees -- a row that
// looks addable but silently does nothing, or one refused for the wrong
// reason -- so it lives here instead.

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

/** `id` defaults to a placement of `def("id", ...)` with the same widget id
 * unless overridden, so a test can place a copy of the widget under test just
 * by reusing its instanceId as the item id's base. */
function item(
  instanceId: string,
  x: number,
  y: number,
  w: number,
  h: number,
  id: string = `w-${instanceId}`,
): LayoutItem {
  return { instanceId, id, x, y, w, h };
}

/** `n` items with distinct instance ids, none of them `widgetId`, sized and
 * positioned so their content is irrelevant -- only the count matters to the
 * item-cap check, which runs before anything else looks at the layout. */
function fillerItems(n: number, widgetId = "filler"): LayoutItem[] {
  return Array.from({ length: n }, (_, i) =>
    item(`${widgetId}-${i}`, 0, i, 1, 1, `${widgetId}-${i}`),
  );
}

describe("disabledReasonFor", () => {
  test("a widget that can be added returns null", () => {
    expect(
      disabledReasonFor(def("nodes", 4, 4), [], DASHBOARD_COLUMNS),
    ).toBeNull();
  });

  test("an unparameterized widget already placed reports ALREADY_PLACED", () => {
    const d = def("cluster-health", 4, 4);
    const placed = [item("a", 0, 0, 4, 4, "cluster-health")];
    expect(disabledReasonFor(d, placed, DASHBOARD_COLUMNS)).toBe(
      ALREADY_PLACED,
    );
  });

  test("a widget with nowhere to fit reports NO_ROOM", () => {
    const d = def("new", 4, 4);
    // Fills the entire grid up to the row cap, so nothing can be placed --
    // same fixture shape as placement_test.ts's "already full to the row cap"
    // case, since this reason is a wrapper around that same scan.
    const placed = [item("a", 0, 0, DASHBOARD_COLUMNS, DASHBOARD_MAX_ROWS)];
    expect(disabledReasonFor(d, placed, DASHBOARD_COLUMNS)).toBe(NO_ROOM);
  });

  test("a full dashboard reports DASHBOARD_FULL", () => {
    const d = def("new", 4, 4);
    const placed = fillerItems(DASHBOARD_MAX_ITEMS);
    expect(disabledReasonFor(d, placed, DASHBOARD_COLUMNS)).toBe(
      DASHBOARD_FULL,
    );
  });

  test("already-placed beats no-room when both apply, without paying for a scan", () => {
    // The whole grid is full (so a scan would also return NO_ROOM), and a
    // copy of this exact unparameterized widget is already on it. The
    // already-placed reason must win: it is the cheap membership check, and
    // it is checked before the placement scan runs at all.
    const d = def("cluster-health", DASHBOARD_COLUMNS, DASHBOARD_MAX_ROWS);
    const placed = [
      item("a", 0, 0, DASHBOARD_COLUMNS, DASHBOARD_MAX_ROWS, "cluster-health"),
    ];
    expect(disabledReasonFor(d, placed, DASHBOARD_COLUMNS)).toBe(
      ALREADY_PLACED,
    );
  });

  test("the item cap beats already-placed and no-room together", () => {
    // A full dashboard (DASHBOARD_MAX_ITEMS items) that also happens to
    // already contain this exact widget and has no free cell for it either --
    // every reason applies, and DASHBOARD_FULL must be the one reported,
    // because it is the most general: a full dashboard refuses this widget
    // for a reason that has nothing to do with the widget itself.
    const d = def("cluster-health", 1, 1);
    const placed = [
      item("cluster-health", 0, 0, 1, 1, "cluster-health"),
      ...fillerItems(DASHBOARD_MAX_ITEMS - 1),
    ];
    expect(placed).toHaveLength(DASHBOARD_MAX_ITEMS);
    expect(disabledReasonFor(d, placed, DASHBOARD_COLUMNS)).toBe(
      DASHBOARD_FULL,
    );
  });

  test("a parameterized widget is never refused for being a duplicate", () => {
    // Two placed copies of the same parameterized widget -- prod beside
    // staging -- is the case `params` exists for. With room left, a third
    // copy is addable.
    const d = def("diagnostics", 2, 2, { params: { namespace: [] } });
    const placed = [
      item("a", 0, 0, 2, 2, "diagnostics"),
      item("b", 2, 0, 2, 2, "diagnostics"),
    ];
    expect(disabledReasonFor(d, placed, DASHBOARD_COLUMNS)).toBeNull();
  });

  test("a parameterized widget is still refused when there is no room", () => {
    const d = def("diagnostics", DASHBOARD_COLUMNS, DASHBOARD_MAX_ROWS, {
      params: { namespace: [] },
    });
    const placed = [
      item("a", 0, 0, DASHBOARD_COLUMNS, DASHBOARD_MAX_ROWS, "diagnostics"),
    ];
    expect(disabledReasonFor(d, placed, DASHBOARD_COLUMNS)).toBe(NO_ROOM);
  });

  test("the columns argument changes the answer", () => {
    // `a` occupies the left half of the grid for the widget's full height.
    // On a twelve-column grid, six columns remain free on the right and the
    // widget fits there. On a six-column grid, `a` alone spans the entire
    // width, so the same layout has nowhere left to place it.
    const d = def("new", 6, 1, { minW: 6 });
    const placed = [item("a", 0, 0, 6, DASHBOARD_MAX_ROWS)];
    expect(disabledReasonFor(d, placed, 12)).toBeNull();
    expect(disabledReasonFor(d, placed, 6)).toBe(NO_ROOM);
  });
});
