/**
 * Dashboard grid geometry.
 *
 * Every decision about where a widget ends up lives here, as a pure function
 * over the layout array. The island above it only translates pointer positions
 * into these calls. That split exists because collision resolution and
 * compaction are where a hand-rolled grid actually fails, and because this repo
 * has no component test harness -- logic outside lib/ is untested by
 * construction.
 *
 * Three rules the whole engine obeys:
 *   1. Vertical gravity. After any change everything falls as far up as it can.
 *      This is what makes a stored layout render identically on every client.
 *   2. Displacement is downward only. Sideways displacement cascades
 *      unpredictably across twelve columns.
 *   3. The dragged item wins; everything else yields. Anything else fights the
 *      pointer.
 *
 * Every function returns a new array of new items and never mutates its input,
 * so a caller can hold the pre-drag layout and restore it on cancel. Results
 * come back in reading order (by y, then x), not input order; render by
 * `instanceId`, never by index.
 */
import type { LayoutItem } from "./types.ts";
import { DASHBOARD_COLUMNS } from "./types.ts";

/** The smallest size a resize may produce. */
export interface Bounds {
  minW: number;
  minH: number;
}

/** The measured, on-screen geometry of the grid, in CSS pixels. */
export interface GridMetrics {
  /** Viewport x of the grid's left edge. */
  left: number;
  /** Viewport y of the grid's top edge. */
  top: number;
  cellWidth: number;
  rowHeight: number;
  gap: number;
}

function clamp(v: number, lo: number, hi: number): number {
  return Math.min(hi, Math.max(lo, v));
}

/** Sorts by row, then column: the order results come back in, and the order
 * keyboard and screen-reader traversal follow. */
export function byReadingOrder(p: LayoutItem, q: LayoutItem): number {
  return p.y - q.y || p.x - q.x;
}

/** True when a and b share any column. Touching edges share none. */
function sharesColumn(a: LayoutItem, b: LayoutItem): boolean {
  return !(a.x + a.w <= b.x || b.x + b.w <= a.x);
}

/** True when a and b share any row. Touching edges share none. */
function sharesRow(a: LayoutItem, b: LayoutItem): boolean {
  return !(a.y + a.h <= b.y || b.y + b.h <= a.y);
}

export function overlaps(a: LayoutItem, b: LayoutItem): boolean {
  return sharesColumn(a, b) && sharesRow(a, b);
}

/**
 * Applies vertical gravity: every item falls until it rests on an item above it
 * that shares a column, or on the top edge.
 *
 * Processing in reading order means an item's blockers are already settled when
 * it is placed, so one pass suffices and the result is idempotent. It also
 * separates items that arrive overlapping, which a stored layout (data from
 * outside this module) can, because each item is placed below everything
 * settled in its columns.
 */
export function compact(items: readonly LayoutItem[]): LayoutItem[] {
  const settled: LayoutItem[] = [];

  for (const it of [...items].sort(byReadingOrder)) {
    let y = 0;
    for (const s of settled) {
      if (sharesColumn(it, s)) y = Math.max(y, s.y + s.h);
    }
    settled.push({ ...it, y });
  }
  return settled.sort(byReadingOrder);
}

/**
 * Places `anchor` where it was put and pushes everything that collides with it
 * downward, transitively, then compacts. The anchor keeps its position through
 * the push -- rule 3 -- and only gravity may lift it.
 */
function resolve(
  items: readonly LayoutItem[],
  anchor: LayoutItem,
): LayoutItem[] {
  const placed: LayoutItem[] = [anchor];
  const others = items
    .filter((i) => i.instanceId !== anchor.instanceId)
    .sort(byReadingOrder);

  // Reading order lets a cascade resolve in one pass: anything an item could be
  // pushed into has already been placed. The inner loop repeats because moving
  // below one placed item can land it on another; y only ever grows and there
  // are finitely many items, so it terminates.
  for (const it of others) {
    let candidate = it;
    let moved = true;
    while (moved) {
      moved = false;
      for (const p of placed) {
        if (overlaps(candidate, p)) {
          candidate = { ...candidate, y: p.y + p.h };
          moved = true;
        }
      }
    }
    placed.push({ ...candidate });
  }

  // Gravity last: displacement can leave holes, and a layout with holes is not
  // the canonical form of itself.
  return compact(placed);
}

function sameLayout(
  a: readonly LayoutItem[],
  b: readonly LayoutItem[],
): boolean {
  return (
    a.length === b.length &&
    a.every((p, i) => {
      const q = b[i];
      return (
        p.instanceId === q.instanceId &&
        p.x === q.x &&
        p.y === q.y &&
        p.w === q.w &&
        p.h === q.h
      );
    })
  );
}

/**
 * Resolves the same placement repeatedly until the layout stops changing.
 *
 * One resolve pass is not always a fixed point. Gravity can lift the anchor
 * above items the push put beneath it; resolving the same request again then
 * finds no collision and lets those items fall back above the anchor. A drag
 * or resize handler repeats the request on every pointermove, so without this
 * the widget would jump while the pointer stands still. Results of `resolve`
 * are in reading order, so an index-wise comparison is exact.
 *
 * `place` rebuilds the anchor from the current copy of the target so each pass
 * asks for the same position and size. The pass cap is a guard, not a limit
 * expected in practice: every case the randomized test and review probes found
 * settles on the second pass.
 */
function settle(
  items: readonly LayoutItem[],
  instanceId: string,
  place: (target: LayoutItem) => LayoutItem,
): LayoutItem[] {
  let current = resolve(items, place(findTarget(items, instanceId)));
  for (let pass = 0; pass < items.length; pass++) {
    const next = resolve(current, place(findTarget(current, instanceId)));
    if (sameLayout(next, current)) return next;
    current = next;
  }
  return current;
}

/** The target item; callers have already checked that it exists. */
function findTarget(
  items: readonly LayoutItem[],
  instanceId: string,
): LayoutItem {
  const found = items.find((i) => i.instanceId === instanceId);
  if (!found)
    throw new Error(`grid: item ${instanceId} vanished during resolve`);
  return found;
}

/**
 * Moves one item to (x, y), snapped to whole cells and clamped into the grid,
 * and resolves the result.
 *
 * A non-finite coordinate is a no-op: a pointer read before layout yields NaN,
 * and a NaN in the layout makes every later comparison false, which silently
 * disables collision handling for that item.
 *
 * Moving down past an item below only takes effect once the requested y
 * reaches that item's bottom edge. A smaller downward move overlaps it during
 * the push phase, gets pushed underneath the anchor, and gravity lifts the
 * anchor back on top during compaction -- so the net effect settles back to
 * where it started. That's intentional: it is what keeps a drag stable when
 * moveItem is re-applied to the current layout on every pointermove (as the
 * planned D8 drag does). A keyboard step that should pass the item below has
 * to compute that landing row itself rather than relying on a one-cell nudge.
 *
 * The result is a fixed point: calling moveItem again with the same request on
 * its own result returns the same layout. It is not history-free, though:
 * neighbors displaced during a drag keep their new order, so dragging away and
 * back does not necessarily restore the starting layout. A caller that must
 * restore (cancel) keeps the pre-drag layout, which this never mutates.
 */
export function moveItem(
  items: readonly LayoutItem[],
  instanceId: string,
  x: number,
  y: number,
): LayoutItem[] {
  const target = items.find((i) => i.instanceId === instanceId);
  if (!target || !Number.isFinite(x) || !Number.isFinite(y)) {
    return items.map((i) => ({ ...i }));
  }

  const col = Math.round(x);
  const row = Math.max(0, Math.round(y));
  return settle(items, instanceId, (t) => ({
    ...t,
    x: clamp(col, 0, Math.max(0, DASHBOARD_COLUMNS - t.w)),
    y: row,
  }));
}

/**
 * Resizes one item to (w, h), snapped to whole cells, clamped to its declared
 * minimum and to the right edge, and resolves the result. x never changes: a
 * resize that also shifts the item reads as a bug.
 *
 * Where the minimum does not fit before the right edge, the edge wins. That is
 * only reachable from a stored layout that already breaks the minimum, and an
 * item hanging outside the grid is worse than one below its minimum width.
 *
 * y can change: narrowing an item off the one it rested on lets gravity lift
 * it. A resize handler that derives the height from the pointer must use the
 * item's current y from the result, not one captured when the drag started.
 * Like moveItem, the result is a fixed point under re-application.
 */
export function resizeItem(
  items: readonly LayoutItem[],
  instanceId: string,
  w: number,
  h: number,
  bounds: Bounds,
): LayoutItem[] {
  const target = items.find((i) => i.instanceId === instanceId);
  if (!target || !Number.isFinite(w) || !Number.isFinite(h)) {
    return items.map((i) => ({ ...i }));
  }

  const width = Math.round(w);
  const height = Math.max(1, bounds.minH, Math.round(h));
  return settle(items, instanceId, (t) => ({
    ...t,
    w: Math.max(1, clamp(width, bounds.minW, DASHBOARD_COLUMNS - t.x)),
    h: height,
  }));
}

/** Translates a viewport point to a grid cell, clamped into the grid. */
export function cellFromPoint(
  px: number,
  py: number,
  m: GridMetrics,
): { x: number; y: number } {
  // Before first layout the grid reports zero-size cells, and a container
  // narrower than its own gaps can report a negative cellWidth. Either
  // divides into Infinity/NaN, or -- undetected -- a nonzero cell out of a
  // zero-size grid. A NaN or wrong coordinate would otherwise propagate into
  // the layout, so any size that is not a finite positive number is treated
  // as "not laid out yet".
  if (
    !(Number.isFinite(m.cellWidth) && m.cellWidth > 0) ||
    !(Number.isFinite(m.rowHeight) && m.rowHeight > 0) ||
    !Number.isFinite(m.left) ||
    !Number.isFinite(m.top) ||
    !Number.isFinite(m.gap)
  ) {
    return { x: 0, y: 0 };
  }

  const colStride = m.cellWidth + m.gap;
  const rowStride = m.rowHeight + m.gap;

  return {
    x: clamp(Math.floor((px - m.left) / colStride), 0, DASHBOARD_COLUMNS - 1),
    y: Math.max(0, Math.floor((py - m.top) / rowStride)),
  };
}

/** Number of rows the layout occupies. */
export function layoutHeight(items: readonly LayoutItem[]): number {
  return items.reduce((max, i) => Math.max(max, i.y + i.h), 0);
}
