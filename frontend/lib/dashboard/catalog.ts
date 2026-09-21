/**
 * Why a catalog entry cannot be added to the dashboard being edited, or null
 * when it can.
 *
 * This module is pure: no DOM, no fetch, no signals (D-10). `WidgetPalette.tsx`
 * is a component and therefore untestable in this repo; the decision it used to
 * make inline -- which of five reasons (or none) applies to one catalog row --
 * is exactly the kind of logic that decision requires to live behind a unit
 * test instead, so it lives here and the component only renders what this
 * returns.
 */
import type { SourceState } from "./data.ts";
import { placeNewWidget } from "./placement.ts";
import type { FamilyStatusKey, LayoutItem, WidgetDef } from "./types.ts";
import { DASHBOARD_MAX_ITEMS } from "./types.ts";
import { featurePresent } from "./widget-state.ts";

/** The layout already holds `DASHBOARD_MAX_ITEMS`, the most a saved layout can
 * carry -- the server refuses a layout past it with `limit_reached`. */
export const DASHBOARD_FULL = "This dashboard is full";
/** A second copy of an unparameterized widget would render the same card
 * twice; a parameterized one may legitimately repeat (prod beside staging). */
export const ALREADY_PLACED = "Already on this dashboard";
/** The layout has no free cell of this widget's size below the row cap. */
export const NO_ROOM = "No room on this dashboard";
/** The widget's family is not installed on this cluster, so the card it would
 * add can only ever say so. */
export const NOT_INSTALLED = "Not installed on this cluster";
/** Either the family's own discovery route refused this account -- so whether
 * the feature is installed is not something we are allowed to find out -- or
 * the widget's route is admin-gated and this session is not an admin. One
 * badge for both, because the user's position is the same: the card this row
 * would add can only ever say "you do not have access". */
export const NOT_PERMITTED = "Not permitted for this account";

/**
 * The resolved family statuses, as the island last read them.
 *
 * A snapshot rather than a lookup into the live cache, because this module is
 * pure. A key that is missing, still in flight, or failed for any reason other
 * than a refusal is simply not an answer, and blocks nothing: refusing a row
 * because eight discovery routes have not come back yet would make the palette's
 * contents depend on request timing, and a transient 500 on a discovery route
 * is not evidence a feature is missing.
 */
export type FamilyStatuses = Readonly<
  Partial<Record<FamilyStatusKey, SourceState>>
>;

/**
 * NOT_PERMITTED, NOT_INSTALLED or null -- from the widget's declared family,
 * or from the session when the widget's route is admin-gated.
 *
 * The admin check comes first and needs no fetch. It is a property of the
 * session rather than of the cluster, so it is known on first paint where a
 * discovery status is not; and a widget that is admin-gated AND declares a
 * family would otherwise be refused for the wrong one of the two, since a
 * non-admin's discovery status is itself likely to be a refusal.
 *
 * `viewerIsAdmin` is a TRI-STATE: null means the session has not answered yet,
 * and blocks nothing. Refusing a row because `/auth/me` is still in flight
 * would make the palette's contents depend on request timing, which is the
 * same rule an unanswered family status already follows below.
 */
function availabilityReason(
  def: WidgetDef,
  statuses: FamilyStatuses,
  viewerIsAdmin: boolean | null,
): string | null {
  if (def.adminOnly === true && viewerIsAdmin === false) return NOT_PERMITTED;
  if (def.familyStatus === undefined) return null;
  const status = statuses[def.familyStatus];
  if (status === undefined) return null;
  // Checked before absence, and not merely as a tiebreak: a refused status
  // carries no payload, so absence is not something we know -- only that this
  // account may not ask.
  if (status.errorKind === "permission") return NOT_PERMITTED;
  if (status.data === null) return null;
  return featurePresent(status.data) ? null : NOT_INSTALLED;
}

/**
 * Why `def` cannot be added to `placed` right now, or null when it can.
 *
 * Five reasons, checked in this order:
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
 * 2. The widget cannot work here at all -- its family is not installed on this
 *    cluster, this account may not read it, or its route is admin-gated and
 *    this session is not an admin. These are facts about the
 *    cluster and the account rather than about the layout, which is why they
 *    outrank both reasons below: a cert-manager widget already on the
 *    dashboard of a cluster with no cert-manager is better described as "not
 *    installed" than as "already on this dashboard", since the second reason
 *    hides the thing the user needs to know about the card they already have.
 *    They sit below the item cap for the same reason step 1 does: a full
 *    dashboard refuses every widget regardless.
 * 3. A second copy would not be meaningful. A parameterized widget (one that
 *    declares `params`) legitimately appears twice -- diagnostics for prod
 *    beside diagnostics for staging -- which is the reason `instanceId` exists
 *    at all. An unparameterized one would render exactly the same card twice,
 *    so it is refused here rather than paying for a placement scan: this is a
 *    cheap membership test, checked before the scan in step 3 for the same
 *    reason step 1 is checked before it.
 * 4. There is nowhere left to put it. `placeNewWidget` is the same placement
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
  familyStatuses: FamilyStatuses = {},
  viewerIsAdmin: boolean | null = null,
): string | null {
  if (placed.length >= DASHBOARD_MAX_ITEMS) {
    return DASHBOARD_FULL;
  }
  const availability = availabilityReason(def, familyStatuses, viewerIsAdmin);
  if (availability !== null) {
    return availability;
  }
  if (def.params === undefined && placed.some((i) => i.id === def.id)) {
    return ALREADY_PLACED;
  }
  return placeNewWidget(placed, def, columns) === null ? NO_ROOM : null;
}
