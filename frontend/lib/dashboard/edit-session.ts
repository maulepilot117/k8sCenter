/**
 * The dashboard editor's state machine.
 *
 * This module is pure: no DOM, no fetch, no signals. Everything the editor
 * decides -- whether there is anything to save, what Cancel restores, what a
 * save writes and which revision it claims -- is decided here, so it can be
 * unit tested without a component harness (see the design spec, D-10).
 *
 * A state machine rather than a boolean, because edit mode carries four things
 * at once: whether editing is active, the layout as loaded, the working copy,
 * and the revision the save must pin. Scattering those across four signals is
 * how you get a Save button that writes a stale revision.
 */
import type { DashboardLayoutConfig, LayoutItem } from "./types.ts";

/**
 * One run of the editor, from "Edit layout" to Save or Cancel.
 *
 * Treat it as immutable: every function here returns a new session rather than
 * reshaping this one, because the caller holds it in a signal and a mutation
 * would not re-render -- and, worse, a mutation that reached `baseline` would
 * make Cancel restore the edits.
 */
export interface EditSession {
  /** The layout as loaded. What Cancel restores and what dirtiness is measured against. */
  readonly baseline: DashboardLayoutConfig;
  /** The layout as arranged so far. What Save writes. */
  readonly working: DashboardLayoutConfig;
  /**
   * The revision the baseline was loaded at, and the claim the save makes
   * about what it is replacing.
   *
   * Held here rather than read from the store at save time: the store's
   * revision moves when anything loads, and a save that picked it up would
   * silently overwrite a layout this session never saw.
   */
  readonly revision: number;
}

function cloneItem(item: LayoutItem): LayoutItem {
  // params is the only nested value; spreading the item alone would leave the
  // copy sharing it, which is exactly the aliasing this clone exists to stop.
  return item.params === undefined
    ? { ...item }
    : { ...item, params: { ...item.params } };
}

function cloneConfig(config: DashboardLayoutConfig): DashboardLayoutConfig {
  return { ...config, items: config.items.map(cloneItem) };
}

/**
 * A layout's identity for comparison: placements only, in a fixed order, with
 * the two spellings of "no parameters" collapsed into one.
 *
 * Sorted by instanceId because the grid re-sorts into reading order during
 * compaction, and a reordered-but-identical array is not a change. Field order
 * is fixed by construction rather than by object key order, which JSON.stringify
 * would otherwise take from whichever path built the item.
 */
function canonicalItems(items: readonly LayoutItem[]): string {
  const rows = items
    .map((i) => {
      const params = i.params ?? {};
      const keys = Object.keys(params).sort();
      return [
        i.instanceId,
        i.id,
        i.x,
        i.y,
        i.w,
        i.h,
        // `undefined` and `{}` both land here as an empty array: a widget that
        // takes no parameters can arrive either way depending on whether it
        // came from the wire, the default layout or a palette insertion, and
        // treating that as an edit would arm Save over an untouched layout.
        keys.map((k) => [k, params[k]]),
      ];
    })
    .sort((a, b) => String(a[0]).localeCompare(String(b[0])));
  return JSON.stringify(rows);
}

/** Opens a session over the layout as loaded, at the revision it carried. */
export function beginEdit(
  config: DashboardLayoutConfig,
  revision: number,
): EditSession {
  return {
    // Both copies are deep and independent, including from the caller's: the
    // config handed in is the store's live `layout` value, which nothing may
    // mutate in place and which a load may replace outright.
    baseline: cloneConfig(config),
    working: cloneConfig(config),
    revision,
  };
}

/**
 * Records a new arrangement.
 *
 * Takes items and nothing else. `schemaVersion`, `scope` and `columns` are not
 * the editor's to change -- a dropped scope would write a layout addressed to
 * the wrong dashboard -- so they are carried from the baseline and there is no
 * parameter through which a caller could alter them.
 */
export function applyChange(
  session: EditSession,
  items: readonly LayoutItem[],
): EditSession {
  return {
    baseline: session.baseline,
    working: { ...session.baseline, items: items.map(cloneItem) },
    revision: session.revision,
  };
}

/**
 * Whether the working copy differs from the layout as loaded.
 *
 * Save is disabled when this is false. A drag away and back again therefore
 * disarms it: an enabled Save that writes nothing trains people to ignore it.
 */
export function isDirty(session: EditSession): boolean {
  return (
    canonicalItems(session.working.items) !==
    canonicalItems(session.baseline.items)
  );
}

/** The layout as loaded. What the grid re-mounts from when the user cancels. */
export function discard(session: EditSession): DashboardLayoutConfig {
  // A copy, so a caller that hands this straight to a grid which reshapes it
  // in place cannot corrupt the session it came from.
  return cloneConfig(session.baseline);
}

/** What to save, and the revision to claim while saving it. */
export function commit(session: EditSession): {
  config: DashboardLayoutConfig;
  revision: number;
} {
  return { config: cloneConfig(session.working), revision: session.revision };
}
