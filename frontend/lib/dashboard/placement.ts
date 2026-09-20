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
import { overlaps } from "./grid.ts";
import type { LayoutItem, WidgetDef } from "./types.ts";
import { DASHBOARD_COLUMNS, DASHBOARD_MAX_ROWS } from "./types.ts";

/** How many base-36 characters of the random suffix the instance id carries. */
const INSTANCE_SUFFIX_LEN = 8;

/**
 * A fresh identity for one placement of a widget.
 *
 * Not the widget id, because a parameterized widget may legitimately appear
 * twice -- diagnostics for prod beside diagnostics for staging -- and not an
 * index, because an index is reused after a removal and a stale render could
 * then key a new widget to a removed one's state.
 *
 * Uses `Math.random` instead of `crypto.randomUUID` -- the latter requires a
 * secure context (HTTPS) and is `undefined` on the HTTP-only homelab
 * deployment this repo ships values for, which made this function throw
 * inside the palette's click handler and turned "Add widget" into a silent
 * no-op. Same convention as `GaugeRing.tsx`'s gradient ids, for the same
 * reason. Eight base-36 characters is still far more entropy than a layout
 * that holds at most `DASHBOARD_MAX_ITEMS` of them needs, and the full id
 * stays well inside the 64-rune bound the server stores it under.
 */
export function newInstanceId(widgetId: string): string {
  return `${widgetId}-${Math.random()
    .toString(36)
    .slice(2, 2 + INSTANCE_SUFFIX_LEN)}`;
}

/**
 * The first cell `w` x `h` fits in, scanning rows top-down then columns
 * left-to-right, or `null` when no such cell exists without crossing
 * `DASHBOARD_MAX_ROWS`.
 *
 * The scan is bounded to `y + h <= DASHBOARD_MAX_ROWS` rather than to
 * `layoutHeight(items)` the way earlier versions of this function did. The
 * unbounded scan always answered -- the row at `layoutHeight(items)` is below
 * every item and therefore free -- but on a layout that already reaches the
 * 200-row cap, that free row is itself past the cap, and the server rejects a
 * config with a placement whose `y + h` exceeds it (`invalid_config` in
 * `backend/internal/preferences/dashboard.go`). Returning that cell traded one
 * unsaveable layout for another, so this returns `null` instead. The caller
 * must not clamp `y` down into the cap to paper over the `null` case: a
 * clamped cell would overlap whatever is already at the cap's edge, which the
 * server also refuses. `moveItem` and `resizeItem` in `grid.ts` bound the same
 * cap for the same reason.
 *
 * The "one row past the bottom is always free" fast path this function relied
 * on still applies whenever that row is below the cap -- the loop reaches it
 * before the bound stops the scan, so a layout with room below the cap keeps
 * finding a fresh row exactly as before.
 */
function firstFit(
  items: readonly LayoutItem[],
  w: number,
  h: number,
  columns: number,
): { x: number; y: number } | null {
  for (let y = 0; y + h <= DASHBOARD_MAX_ROWS; y++) {
    for (let x = 0; x + w <= columns; x++) {
      const candidate = { instanceId: "", id: "", x, y, w, h };
      if (!items.some((i) => overlaps(candidate, i))) return { x, y };
    }
  }
  return null;
}

/**
 * Places a newly added widget on `items` and returns it, or `null` when the
 * layout has no room left below `DASHBOARD_MAX_ROWS` for a widget this size.
 * The input is not modified; the caller appends the result and must treat
 * `null` as "cannot place this widget" rather than substituting a clamped
 * position -- see `firstFit` for why a clamp is worse than the `null` it
 * would be papering over.
 *
 * `columns` comes from the layout being edited rather than from the constant,
 * because the stored config carries its own column count and a placement wider
 * than that one is off the grid the server will validate it against.
 */
export function placeNewWidget(
  items: readonly LayoutItem[],
  def: WidgetDef,
  columns: number = DASHBOARD_COLUMNS,
): LayoutItem | null {
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

  const cell = firstFit(items, w, h, columns);
  if (!cell) return null;

  return {
    instanceId: newInstanceId(def.id),
    id: def.id,
    ...cell,
    w,
    h,
  };
}
