/**
 * Where a widget added from the catalog palette lands.
 *
 * This module is pure: no DOM, no fetch, no signals (D-10). The palette is a
 * component and therefore untestable in this repo; the decision it delegates
 * here -- which cell a new widget occupies -- is the part a user notices when
 * it is wrong, so it lives behind a unit test instead.
 *
 * The rule is first fit in reading order: scan rows from the top, columns from
 * the left, and take the first cell where the widget fits without touching
 * anything already placed. That fills the holes a resize left behind before it
 * starts a new row, which matters because a widget appended at the bottom of a
 * long dashboard is off screen -- the user clicks Add and appears to get
 * nothing back. The island scrolls to whatever this returns, but landing in
 * view beats being scrolled to.
 *
 * It deliberately does not displace anything. Every other write path in the
 * editor (`moveItem`, `resizeItem`) pushes neighbours down because the user is
 * dragging a widget somewhere specific; an insertion has no such intent, and
 * rearranging the dashboard around a widget the user has not looked at yet is
 * a worse surprise than an extra row.
 */
import { layoutHeight, overlaps } from "./grid.ts";
import type { LayoutItem, WidgetDef } from "./types.ts";
import { DASHBOARD_COLUMNS } from "./types.ts";

/** How many hex characters of the uuid the instance id carries. */
const INSTANCE_SUFFIX_LEN = 8;

/**
 * A fresh identity for one placement of a widget.
 *
 * Not the widget id, because a parameterized widget may legitimately appear
 * twice -- diagnostics for prod beside diagnostics for staging -- and not an
 * index, because an index is reused after a removal and a stale render could
 * then key a new widget to a removed one's state.
 *
 * `crypto.randomUUID` is available in every browser this app supports and in
 * the test runtime. Eight hex characters is 4 billion values against a layout
 * that holds at most `DASHBOARD_MAX_ITEMS` of them, and the full id stays well
 * inside the 64-rune bound the server stores it under.
 */
export function newInstanceId(widgetId: string): string {
  return `${widgetId}-${crypto.randomUUID().slice(0, INSTANCE_SUFFIX_LEN)}`;
}

/**
 * The first cell `w` x `h` fits in, scanning rows top-down then columns
 * left-to-right, or the top-left of a new row below everything when none does.
 */
function firstFit(
  items: readonly LayoutItem[],
  w: number,
  h: number,
  columns: number,
): { x: number; y: number } {
  // `layoutHeight` is the lowest edge of the layout, so the row at `lastRow`
  // is below every item and its first cell is free -- which is what bounds the
  // scan and guarantees it answers, PROVIDED `w <= columns` so the inner loop
  // runs at all. The caller clamps for exactly that reason.
  const lastRow = layoutHeight(items);
  for (let y = 0; y <= lastRow; y++) {
    for (let x = 0; x + w <= columns; x++) {
      const candidate = { instanceId: "", id: "", x, y, w, h };
      if (!items.some((i) => overlaps(candidate, i))) return { x, y };
    }
  }
  // Unreachable while that clamp holds, and deliberately not a throw: a future
  // caller asking for a widget wider than the grid should get the one corner
  // that is always empty rather than an exception thrown from a click handler.
  return { x: 0, y: lastRow };
}

/**
 * Places a newly added widget on `items` and returns it. The input is not
 * modified; the caller appends the result.
 *
 * `columns` comes from the layout being edited rather than from the constant,
 * because the stored config carries its own column count and a placement wider
 * than that one is off the grid the server will validate it against.
 */
export function placeNewWidget(
  items: readonly LayoutItem[],
  def: WidgetDef,
  columns: number = DASHBOARD_COLUMNS,
): LayoutItem {
  // The minimum wins over a smaller default: both come from the same
  // registration, so disagreeing is a catalog bug, but the editor refuses to
  // resize below minW/minH and the server refuses to store a placement under
  // them -- emitting one would produce a widget that cannot be saved and
  // cannot be shrunk to explain why.
  //
  // The grid width wins over both: a widget wider than the grid is clamped
  // rather than dropped, because an Add that silently does nothing is the one
  // outcome the palette must not have. A clamp below minW is possible only on
  // a grid narrower than a widget's own minimum, which no shipped scope has.
  const w = Math.max(1, Math.min(Math.max(def.defaultW, def.minW), columns));
  const h = Math.max(1, Math.max(def.defaultH, def.minH));

  return {
    instanceId: newInstanceId(def.id),
    id: def.id,
    ...firstFit(items, w, h, columns),
    w,
    h,
  };
}
