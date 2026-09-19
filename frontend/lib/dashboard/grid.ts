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
import {
  DASHBOARD_COLUMNS,
  DASHBOARD_GRID_GAP,
  DASHBOARD_MAX_ROWS,
  DASHBOARD_ROW_HEIGHT,
} from "./types.ts";

/** A whole-cell position on the grid: column, then row. */
export interface Cell {
  x: number;
  y: number;
}

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
 * pointer drag does). A keyboard step that should pass the item below has
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
    // Bounded below the row cap the same way x is bounded below the column
    // count, so a drag cannot place an item the server would then refuse.
    y: clamp(row, 0, Math.max(0, DASHBOARD_MAX_ROWS - t.h)),
  }));
}

/**
 * Moves one item by a single keyboard step: one whole cell, in one direction.
 *
 * Three of the four directions are ordinary `moveItem` requests. Down is not.
 * `moveItem` documents that a downward request short of the neighbour's bottom
 * edge settles back where it started -- the push puts the neighbour under the
 * anchor, and gravity lifts the anchor back on top. That is what keeps a
 * pointer drag stable as the pointer crosses a neighbour, and it is exactly
 * wrong for a key press, which has no later event to correct it: the widget
 * would simply not move, however many times the user pressed the key.
 *
 * So a downward step asks for successively lower rows and takes the first one
 * the engine honors, which lands the item directly below whatever it was asked
 * to pass. `moveItem` never places an item lower than asked, so that is also
 * the smallest downward move available -- the step stays a step. The scan is
 * bounded by the layout's own height, below which nothing can block; an item
 * already at the bottom of its column stack finds no row that changes anything
 * and correctly stays where it is.
 *
 * `dx` and `dy` are a single arrow key, so at most one is non-zero. A request
 * that moves sideways is honored as asked and never escalated: a column change
 * is always applied, so "nothing happened" cannot be a downward absorption.
 *
 * A sideways step changes the column it was asked for, and may change the row
 * as well. Rule 1 is that everything falls as far up as it can after any
 * change, so stepping out of the columns that were holding an item down lets
 * gravity lift it -- possibly several rows, from one key press. This is not a
 * keyboard quirk: the same press-and-drag one column over does the same thing,
 * and a layout with a hole left where the item was is not the canonical form of
 * itself, so there is no "keep the row" placement to offer that the next
 * operation would not undo. Two consequences worth knowing:
 *
 *   - the step is not reversible key-for-key. Like `moveItem`, from which this
 *     inherits it, stepping away and back can settle somewhere the layout has
 *     not been; that behavior has its own test.
 *   - the caller must announce where the item actually landed rather than
 *     where it asked to go, which is why `DashboardGrid` reads the position
 *     back out of the result instead of predicting it.
 */
export function stepItem(
  items: readonly LayoutItem[],
  instanceId: string,
  dx: number,
  dy: number,
): LayoutItem[] {
  const target = items.find((i) => i.instanceId === instanceId);
  if (!target || !Number.isFinite(dx) || !Number.isFinite(dy)) {
    return items.map((i) => ({ ...i }));
  }

  const col = Math.round(dx);
  const row = Math.round(dy);
  const first = moveItem(items, instanceId, target.x + col, target.y + row);
  if (col !== 0 || row <= 0) return first;
  if (findTarget(first, instanceId).y > target.y) return first;

  const floor = layoutHeight(items);
  for (let y = target.y + row + 1; y <= floor; y++) {
    const next = moveItem(items, instanceId, target.x, y);
    if (findTarget(next, instanceId).y > target.y) return next;
  }
  return first;
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
    // Height is clamped against the row cap for the same reason width is
    // clamped against the column count: the server refuses a placement whose
    // y+h passes it, so letting the editor build one produces a rejected save
    // the user cannot connect to anything they did. Never below 1 -- a
    // target already at the cap has no room left, and a zero-height widget is
    // worse than one that refuses to grow.
    h: Math.max(1, Math.min(height, DASHBOARD_MAX_ROWS - t.y)),
  }));
}

/**
 * Resizes one item by a whole cell in one direction -- a keyboard resize step.
 *
 * Thin on purpose. The clamping, the fixed x and the settling are all
 * `resizeItem`'s; this exists so a caller never has to read an item's current
 * size in order to change it, which is the same split the pointer path gets
 * from `resizeItemToCell`.
 */
export function resizeItemBy(
  items: readonly LayoutItem[],
  instanceId: string,
  dw: number,
  dh: number,
  bounds: Bounds,
): LayoutItem[] {
  const target = items.find((i) => i.instanceId === instanceId);
  if (!target) return items.map((i) => ({ ...i }));
  return resizeItem(items, instanceId, target.w + dw, target.h + dh, bounds);
}

/**
 * How many times `resizeItemToCell` re-aims within one call. Two settles every
 * case the tests and review probes found; the rest is headroom.
 */
const RESIZE_AIM_PASSES = 4;

/**
 * Resizes one item so its bottom-right corner covers `cell`, and settles the
 * result.
 *
 * The size is measured from the item's own top-left and is inclusive of `cell`:
 * releasing on the corner cell an item already occupies means "unchanged", not
 * "one cell". Clamping is `resizeItem`'s, so the declared minimum and the right
 * edge are honored here too.
 *
 * It applies more than once when it has to. Narrowing an item off the one it
 * rested on lets gravity lift it, and the height just applied was measured from
 * the y it had *before* that lift -- leaving the bottom edge stranded above the
 * pointer. A caller re-applying on every pointermove would eventually correct
 * it, which is the problem: whether the correction happened at all depended on
 * whether one more event arrived before the release, so the same pointer
 * position committed two different sizes. Re-aiming here makes the result a
 * function of `cell` alone.
 *
 * That holds whether or not the aim converges: the pass count is fixed, so the
 * same layout and cell always produce the same output. In practice it settles
 * on the second pass, because the only thing changing after the first is the
 * height, and a height change cannot alter which columns the item occupies.
 */
export function resizeItemToCell(
  items: readonly LayoutItem[],
  instanceId: string,
  cell: Cell,
  bounds: Bounds,
): LayoutItem[] {
  let current = items.find((i) => i.instanceId === instanceId);
  if (!current) return items.map((i) => ({ ...i }));

  let out = resizeItem(
    items,
    instanceId,
    cell.x - current.x + 1,
    cell.y - current.y + 1,
    bounds,
  );
  for (let pass = 1; pass < RESIZE_AIM_PASSES; pass++) {
    const settled = findTarget(out, instanceId);
    // x cannot move during a resize, so only a lift needs a second look.
    if (settled.y === current.y) return out;
    current = settled;
    out = resizeItem(
      out,
      instanceId,
      cell.x - current.x + 1,
      cell.y - current.y + 1,
      bounds,
    );
  }
  return out;
}

/** Translates a viewport point to a grid cell, clamped into the grid. */
export function cellFromPoint(px: number, py: number, m: GridMetrics): Cell {
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

/**
 * Derives the grid's cell geometry from its measured box.
 *
 * The columns share whatever the gaps leave, so the cell width follows the
 * container and only the row height is fixed. A container narrower than its
 * own gaps yields a negative cell width; that is left as measured, because
 * `cellFromPoint` is where "not laid out yet" is decided and a clamp here
 * would hide it.
 */
export function metricsFrom(rect: {
  left: number;
  top: number;
  width: number;
}): GridMetrics {
  const gap = DASHBOARD_GRID_GAP;
  return {
    left: rect.left,
    top: rect.top,
    cellWidth: (rect.width - gap * (DASHBOARD_COLUMNS - 1)) / DASHBOARD_COLUMNS,
    rowHeight: DASHBOARD_ROW_HEIGHT,
    gap,
  };
}

/**
 * Where a dragged item should sit, given where it started and how far the
 * pointer has travelled in cells.
 *
 * Translating the item by the pointer's displacement, rather than putting its
 * corner under the pointer, is what lets a drag start anywhere on the title
 * bar without the widget jumping. `start` is the item's position when the drag
 * began, not its current one: the item can be lifted by gravity mid-drag, and
 * feeding that back in would make the widget walk away from the pointer.
 */
export function dragTarget(start: Cell, origin: Cell, cell: Cell): Cell {
  return {
    x: start.x + (cell.x - origin.x),
    y: start.y + (cell.y - origin.y),
  };
}

/** Number of rows the layout occupies. */
export function layoutHeight(items: readonly LayoutItem[]): number {
  return items.reduce((max, i) => Math.max(max, i.y + i.h), 0);
}

/** A layout item paired with the widget definition it resolved to. */
export interface Renderable<TDef> {
  item: LayoutItem;
  def: TDef;
}

/**
 * Pairs each item with its widget definition, drops the ones no definition
 * answers for, and returns what is left compacted, in reading order.
 *
 * A stored layout can name a widget this build does not have -- a retired id,
 * or one from a newer release. Skipping it is the display half of that
 * contract (telling the user belongs to the editor). Gravity runs after the
 * skip, not before, or the dropped widget would leave its rows behind as an
 * empty band: the grid's own rule 1 says everything falls as far up as it can,
 * and a hole is not the canonical form of a layout.
 *
 * The lookup is a parameter rather than an import so this stays a pure
 * function of its inputs, which is what makes the skip testable at all.
 */
export function resolveRenderable<TDef>(
  items: readonly LayoutItem[],
  lookup: (id: string) => TDef | undefined,
): Renderable<TDef>[] {
  const byInstance = new Map<string, TDef>();
  const known: LayoutItem[] = [];

  for (const it of items) {
    const def = lookup(it.id);
    if (def === undefined) continue;
    byInstance.set(it.instanceId, def);
    known.push(it);
  }

  return compact(known).flatMap((item) => {
    const def = byInstance.get(item.instanceId);
    return def === undefined ? [] : [{ item, def }];
  });
}
