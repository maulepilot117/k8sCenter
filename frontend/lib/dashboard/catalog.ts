/**
 * Why a catalog entry cannot be added to the dashboard being edited, or null
 * when it can.
 *
 * This module is pure: no DOM, no fetch, no signals (D-10). `WidgetPalette.tsx`
 * is a component and therefore untestable in this repo; the decision it used to
 * make inline -- which of three reasons (or none) applies to one catalog row --
 * is exactly the kind of logic that decision requires to live behind a unit
 * test instead, so it lives here and the component only renders what this
 * returns.
 */
import { placeNewWidget } from "./placement.ts";
import type { LayoutItem, WidgetDef } from "./types.ts";
import { DASHBOARD_MAX_ITEMS } from "./types.ts";

/** The layout already holds `DASHBOARD_MAX_ITEMS`, the most a saved layout can
 * carry -- the server refuses a layout past it with `limit_reached`. */
export const DASHBOARD_FULL = "This dashboard is full";
/** A second copy of an unparameterized widget would render the same card
 * twice; a parameterized one may legitimately repeat (prod beside staging). */
export const ALREADY_PLACED = "Already on this dashboard";
/** The layout has no free cell of this widget's size below the row cap. */
export const NO_ROOM = "No room on this dashboard";

/**
 * Why `def` cannot be added to `placed` right now, or null when it can.
 *
 * Three reasons, checked in this order:
 *
 * 1. The dashboard is already at `DASHBOARD_MAX_ITEMS`. This is checked first
 *    and reported ahead of the per-widget reasons below because it is the most
 *    general: a full dashboard refuses every widget, parameterized or not,
 *    regardless of whether a copy is already placed or a cell is free for it.
 *    Reporting a narrower reason (e.g. "no room") on a full dashboard would be
 *    true by coincidence and wrong the moment a widget were removed and the
 *    cap no longer applied to it specifically. It is also the cheapest check
 *    -- a length comparison -- so paying for it first costs nothing on the
 *    common case where the dashboard is nowhere near full.
 * 2. A second copy would not be meaningful. A parameterized widget (one that
 *    declares `params`) legitimately appears twice -- diagnostics for prod
 *    beside diagnostics for staging -- which is the reason `instanceId` exists
 *    at all. An unparameterized one would render exactly the same card twice,
 *    so it is refused here rather than paying for a placement scan: this is a
 *    cheap membership test, checked before the scan in step 3 for the same
 *    reason step 1 is checked before it.
 * 3. There is nowhere left to put it. `placeNewWidget` is the same placement
 *    scan the palette's Add handler runs when the button is actually pressed;
 *    running it here too means the catalog never offers a row whose Add would
 *    silently fail to place.
 *
 * `columns` is the edited layout's own, passed down from the caller, so this
 * check and the insertion behind the palette's Add handler ask the same
 * question of the same grid -- a widget that fits on a wider grid than the one
 * actually being edited would be offered a row whose Add then does nothing.
 */
export function disabledReasonFor(
  def: WidgetDef,
  placed: readonly LayoutItem[],
  columns: number,
): string | null {
  if (placed.length >= DASHBOARD_MAX_ITEMS) {
    return DASHBOARD_FULL;
  }
  if (def.params === undefined && placed.some((i) => i.id === def.id)) {
    return ALREADY_PLACED;
  }
  return placeNewWidget(placed, def, columns) === null ? NO_ROOM : null;
}
