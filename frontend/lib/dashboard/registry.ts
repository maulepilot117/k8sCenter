/**
 * The widget catalog.
 *
 * This is the allowlist the server validates a stored layout against and the
 * catalog the editor palette renders. It is deliberately a module-level
 * registration table rather than a dynamic plugin surface: a layout naming a
 * widget this build does not have must be a detectable condition, not an
 * import.
 *
 * Widget components register themselves here at module load. The rendering
 * modules import their widgets so registration happens before first paint.
 */
import { KNOWN_PARAM_KEYS } from "./params.ts";
import type { DashboardScope, WidgetDef } from "./types.ts";

const widgets = new Map<string, WidgetDef>();

/**
 * Ids that existed in a shipped release and no longer do.
 *
 * Retirement is permanent and ids are never reused: a stored layout still
 * naming one must be recognised as retired (dropped quietly on read) rather
 * than as a typo (rejected on write).
 *
 * This is a frozen array rather than a frozen Set on purpose. `Object.freeze`
 * does not stop `Set.prototype.add` -- it seals own properties, while a Set's
 * contents live in internal slots -- so a frozen Set would document an
 * immutability guarantee it does not actually have. A frozen array throws on
 * mutation in module (strict) code, which is the guarantee we want. Lookup
 * goes through the Set built from it below, which is never exported.
 */
export const RETIRED_WIDGET_IDS: readonly string[] = Object.freeze(
  [] as string[],
);

const retiredIds = new Set<string>(RETIRED_WIDGET_IDS);

export function isRetiredWidgetId(id: string): boolean {
  return retiredIds.has(id);
}

/** Registers a widget. Throws on a duplicate or retired id, because both are
 * programming errors that would otherwise surface as a silently missing or
 * silently wrong widget. */
export function registerWidget(def: WidgetDef): void {
  if (widgets.has(def.id)) {
    throw new Error(`duplicate widget id ${def.id}`);
  }
  if (isRetiredWidgetId(def.id)) {
    throw new Error(`widget id ${def.id} is retired and cannot be reused`);
  }
  // The registry test asserts this too, but the test suite is not the runtime:
  // a definition registered from anywhere must satisfy it. pickMode falls back
  // toward "normal" from both directions and returns it for an empty mode list
  // rather than undefined, so a widget missing "normal" would be handed a mode
  // it does not implement -- the one thing the display-mode contract promises
  // cannot happen.
  if (!def.modes.includes("normal")) {
    throw new Error(
      `widget ${def.id} must implement the "normal" display mode`,
    );
  }
  // A parameter key the server does not recognise is the one defect in this
  // file that produces no visible symptom at all. The read path recognises a
  // namespace by an exact key and re-authorizes its value on every read (R5);
  // a widget that declared `ns` instead would store, render and save exactly
  // as this one does, and simply never be withheld from a user who lost access
  // to the namespace it reads. There is nothing downstream to catch that, so
  // it is caught here.
  for (const key of Object.keys(def.params ?? {})) {
    if (!KNOWN_PARAM_KEYS.includes(key)) {
      throw new Error(
        `widget ${def.id} declares unknown parameter ${key}; ` +
          `the server recognises ${KNOWN_PARAM_KEYS.join(", ")}`,
      );
    }
  }
  widgets.set(def.id, def);
}

/** Total lookup: an unknown id is undefined, never a throw. Reads drop unknown
 * ids with a notice, so the caller needs a value to test. */
export function getWidget(id: string): WidgetDef | undefined {
  return widgets.get(id);
}

export function allWidgets(): WidgetDef[] {
  return [...widgets.values()];
}

export function widgetsForScope(scope: DashboardScope): WidgetDef[] {
  return allWidgets().filter((w) => w.scopes.includes(scope));
}
