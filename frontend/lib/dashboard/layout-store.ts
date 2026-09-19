/**
 * Client-only module — MUST NOT be imported in server-rendered components.
 *
 * It holds module-level signals, which are a process-global singleton under
 * SSR: imported during a render, one request's layout would be visible to the
 * next one's. It also imports lib/preferences.ts, whose own banner says the
 * same thing about the access token behind it. Import this from islands only.
 * This module is named directly in the SSR guard's FORBIDDEN_MODULES
 * (server/check-no-signal-store-in-ssr.ts), like every other signal store --
 * it would also be caught transitively through lib/preferences.ts to
 * lib/api.ts, but that coverage would vanish with the import that carries it.
 *
 * Deliberately NOT localStorage-backed, for the reason pin-store.ts:9-13
 * records: a layout is server state — the whole point is that it follows the
 * user to another browser — and any localStorage key here would additionally
 * be captured into Playwright's storageState and leak across the entire E2E
 * suite.
 *
 * The decision logic is split out as two pure functions, `dropUnknownWidgets`
 * and `layoutFromResponse`, so the rules that matter — what an unsaved scope
 * means, what a filtered response means, which placements survive — are unit
 * tested without a transport seam. lib/preferences.ts has no injectable fetch
 * (its own header says why), so `loadLayout` and `saveLayout` are tested by
 * stubbing `globalThis.fetch` around a real call, the way preferences_test.ts
 * does; that is what covers the ordering rules below, which no pure function
 * can express.
 */
import { signal } from "@preact/signals";
import {
  type LayoutResponse,
  type PreferenceReason,
  preferenceReason,
  preferencesApi,
} from "@/lib/preferences.ts";
import { DEFAULT_OVERVIEW_LAYOUT } from "./default-layout.ts";
import { getWidget } from "./registry.ts";
import type {
  DashboardLayoutConfig,
  DashboardScope,
  LayoutItem,
} from "./types.ts";

/**
 * The layout each scope falls back to when the user has not saved one.
 *
 * A `Record` over the scope union rather than a lookup with a default: adding
 * a scope to DASHBOARD_SCOPES without a default for it should fail to compile,
 * not silently serve the overview dashboard's arrangement.
 */
const DEFAULT_LAYOUTS: Record<DashboardScope, DashboardLayoutConfig> = {
  overview: DEFAULT_OVERVIEW_LAYOUT,
};

/**
 * The layout the grid renders. Starts at the default so the first paint has
 * something real, and stays there when a load fails.
 */
export const layout = signal<DashboardLayoutConfig>(DEFAULT_OVERVIEW_LAYOUT);

/** True once a load has settled — including the 204 that means "not saved". */
export const layoutLoaded = signal(false);

/**
 * Why a load failed, when one did. `undefined` means no load has failed.
 *
 * `"unknown"` rather than `undefined` for a failure carrying no reason code —
 * a dropped connection, an abort-free 500 — because this module's whole point
 * is that an absent observation must not look like an empty one (R3), and a
 * signal where `undefined` meant both "fine" and "failed, cannot say why"
 * would break that rule in the one place it is being enforced. pin-store.ts
 * has the narrower type and the ambiguity that comes with it; its callers work
 * around it by testing for one named reason and ignoring the rest.
 *
 * The editor reads this to stay read-only: offering a Save button over a
 * layout the server could not be asked about invites a write nobody can
 * honour.
 */
export type LayoutUnavailable = PreferenceReason | "unknown";

export const layoutUnavailable = signal<LayoutUnavailable | undefined>(
  undefined,
);

/**
 * The revision the stored layout carried, or 0 when nothing is stored.
 *
 * This is the claim the next save makes about what it is replacing, and both
 * values are checked: 0 against an existing layout is a conflict, not an
 * overwrite, and so is a non-zero revision against a scope with no layout.
 */
export const layoutRevision = signal(0);

/**
 * instanceIds the server removed from the last read because the caller can no
 * longer see their namespace.
 *
 * Non-empty makes the loaded layout unsafe to write back: the response carries
 * the record's *unchanged* revision, so a read-modify-write would pass the
 * concurrency check and delete those placements permanently. `saveLayout`
 * refuses rather than leaving that to each caller to remember.
 */
export const layoutWithheld = signal<string[]>([]);

/** What the last load had to change, in words a user can act on. */
export const layoutWarnings = signal<string[]>([]);

/**
 * Bumped once per completed load, and never by a save.
 *
 * The grid takes its starting layout as a mount-time prop and reshapes its own
 * copy from there, so a load that lands after mount has to re-mount it; this
 * is the key that does it. A save must not bump it: the grid is already
 * showing exactly what was written, and re-mounting there would discard the
 * user's selection and scroll position for no change at all.
 */
export const layoutGeneration = signal(0);

/** Raised instead of writing back a layout the server filtered on the way out. */
export class WithheldLayoutError extends Error {
  constructor(public readonly withheld: string[]) {
    super(
      `refusing to save: the server withheld ${withheld.length} placement(s) ` +
        `from this layout (${withheld.join(", ")}), and saving it now would ` +
        `delete them permanently`,
    );
    this.name = "WithheldLayoutError";
  }
}

export interface DroppedLayout {
  config: DashboardLayoutConfig;
  warnings: string[];
}

/**
 * Removes placements naming a widget this build does not have, and says which.
 *
 * This is the read half of the unknown-id contract: the server rejects an
 * unknown id on write, because there it is a typo or a client bug, and drops
 * nothing on read, because a layout stored by a newer build is a legitimate
 * thing for an older one to receive. Rendering is already total —
 * `resolveRenderable` skips what it cannot look up — so what this adds is the
 * notice. A widget that disappears without a word reads as a bug in the
 * dashboard rather than as a missing build.
 *
 * A retired id and an id from a future build are treated identically, and
 * deliberately: both come back from `getWidget` as undefined, RETIRED_WIDGET_IDS
 * is empty today, and a separate retired branch could not be made to fail a
 * test. A guard that cannot go red is decoration. Split the message when the
 * first id is actually retired and the branch becomes reachable.
 *
 * Returns `{ config, warnings }` like `applyViewState`, whose callers are
 * required to surface the warnings (R3).
 */
export function dropUnknownWidgets(
  config: DashboardLayoutConfig,
): DroppedLayout {
  const kept: LayoutItem[] = [];
  // One warning per distinct widget id, not per placement. A parameterized
  // widget may legitimately sit on the dashboard twice, and two identical
  // sentences tell the user nothing the first did not -- they also collide as
  // list keys wherever the warnings are rendered, which is a rendering bug
  // rather than a wording one. A Set preserves first-encounter order.
  const missing = new Set<string>();

  for (const it of config.items) {
    if (getWidget(it.id) === undefined) {
      missing.add(it.id);
      continue;
    }
    kept.push(it);
  }

  const warnings = [...missing].map(
    (id) =>
      `Removed "${id}" from this dashboard: this build has no such widget.`,
  );

  // Dropping everything yields an empty layout, never the shipped default.
  // Substituting the default would discard an arrangement the user spent time
  // on because one build was missing its widgets, and the next save would
  // write that substitution over their work.
  return { config: { ...config, items: kept }, warnings };
}

export interface LoadedLayout {
  config: DashboardLayoutConfig;
  revision: number;
  withheld: string[];
  warnings: string[];
}

/**
 * Turns what the read endpoint returned into what the store should hold.
 *
 * `null` is the 204: the user has not customized this dashboard, so the answer
 * is the shipped default at revision 0 — the claim "I believe none exists",
 * which is what the first save has to send.
 */
export function layoutFromResponse(
  res: LayoutResponse | null,
  scope: DashboardScope,
): LoadedLayout {
  if (res === null) {
    return {
      config: DEFAULT_LAYOUTS[scope],
      revision: 0,
      withheld: [],
      warnings: [],
    };
  }

  const { config, warnings } = dropUnknownWidgets(res.config);
  return {
    config,
    revision: res.revision,
    // `withheld` is omitempty on the wire, so an unfiltered response has no
    // such key. Normalizing here is what keeps every caller from having to.
    withheld: res.withheld ?? [],
    warnings,
  };
}

let inFlight: AbortController | null = null;

/**
 * The scope the signals above currently describe, or null before any load.
 *
 * `layoutRevision` and `layoutWithheld` are the two things a save reads, and
 * neither carries the scope it was observed for. Without this, a save for one
 * scope would claim another scope's revision and consult another scope's
 * withheld list -- and the withheld list is the only thing standing between a
 * revoked namespace and permanent deletion.
 */
let loadedScope: DashboardScope | null = null;

/**
 * Bumped by every committed write of the signals above.
 *
 * Loads and saves both write the same state, and a read is not cancelled by a
 * write, so a GET issued before a successful PUT can resolve after it and
 * restore the older config and revision. The next save would then claim a
 * revision the server has already moved past and be refused, while the screen
 * showed a layout the user had in fact already saved. A load captures this
 * counter before awaiting and discards its own result if anything committed
 * while it was in flight.
 */
let commitSeq = 0;

/** Raised instead of saving state that describes a different scope. */
export class StaleLayoutScopeError extends Error {
  constructor(
    public readonly requested: DashboardScope,
    public readonly observed: DashboardScope | null,
  ) {
    super(
      `refusing to save scope "${requested}": the store holds observations ` +
        `for ${observed === null ? "no scope yet" : `"${observed}"`}, so its ` +
        `revision and withheld list do not describe what is being written`,
    );
    this.name = "StaleLayoutScopeError";
  }
}

/**
 * Loads the caller's layout for one scope.
 *
 * On failure `layout` is left exactly as it was: a transient error must not
 * blank a dashboard the user was looking at a moment ago. The caller renders
 * the unavailable state from `layoutUnavailable` and keeps the grid read-only
 * while it is set.
 */
export async function loadLayout(
  scope: DashboardScope,
  signal?: AbortSignal,
): Promise<void> {
  // A caller whose signal is already aborted is asking for nothing. Falling
  // through would issue a request only to discard it, and -- worse -- would
  // abort a healthy load already in flight on the way past.
  if (signal?.aborted) return;

  // Cancel any previous load so a slow response cannot land after a newer one
  // and overwrite it with staler state.
  //
  // One controller for the module, not one per scope, because exactly one
  // scope ships. A second scope makes this wrong rather than merely coarse:
  // two dashboards loading at once would abort each other. Key this by scope
  // in the unit that adds the second one -- doing it now would be machinery no
  // test could reach.
  inFlight?.abort();
  const ac = new AbortController();
  inFlight = ac;
  // Captured before the await: see `commitSeq`.
  const startedAt = commitSeq;

  const onAbort = () => ac.abort();
  signal?.addEventListener("abort", onAbort, { once: true });

  try {
    const res = await preferencesApi.getLayout(scope, ac.signal);
    if (ac.signal.aborted) return;
    // A save committed while this read was in flight. The read describes the
    // state before that write, so applying it now would roll the write back.
    if (commitSeq !== startedAt) return;

    const loaded = layoutFromResponse(res, scope);
    // Only a load that actually replaces the rendered layout counts as a new
    // generation. The overwhelmingly common case is a 204 over a store still
    // holding the default, where `layoutFromResponse` hands back that very
    // object -- and re-mounting the grid for a layout identical to the one it
    // is already showing would throw away a drag the user had begun before the
    // response landed, for no change at all.
    //
    // Reference equality, so it only skips the case it can prove. A load that
    // DID find a stored layout always rebuilds the config and so always counts
    // as replaced, even for identical content: today nothing can observe that,
    // because the only caller loads once on mount. A reload affordance would,
    // and would want a content comparison here rather than this one.
    const replaced = loaded.config !== layout.value;

    loadedScope = scope;
    commitSeq += 1;
    layout.value = loaded.config;
    layoutRevision.value = loaded.revision;
    layoutWithheld.value = loaded.withheld;
    layoutWarnings.value = loaded.warnings;
    layoutLoaded.value = true;
    layoutUnavailable.value = undefined;
    if (replaced) layoutGeneration.value += 1;
  } catch (err) {
    if (
      ac.signal.aborted ||
      (err instanceof DOMException && err.name === "AbortError")
    ) {
      return;
    }
    layoutUnavailable.value = preferenceReason(err) ?? "unknown";
    // A failure settles the load: the caller has its answer, which is that
    // there isn't one. Leaving this false would make "still loading" and
    // "asked, and could not be told" the same state to every consumer.
    layoutLoaded.value = true;
    // layout.value is intentionally untouched: it is either the default the
    // module started at or the last layout that did load, and both are better
    // than an empty dashboard. loadedScope likewise stays as it was -- a
    // failed read observed nothing, so it cannot claim to describe this scope.
  } finally {
    signal?.removeEventListener("abort", onAbort);
    if (inFlight === ac) inFlight = null;
  }
}

/**
 * Saves a layout for one scope, claiming the revision the last load observed.
 *
 * Refuses outright when that load came back filtered. The server enforces
 * nothing here — the response carried the record's unchanged revision, so the
 * write would succeed and take the withheld placements with it — which makes
 * this the client's obligation and the one place it can be kept.
 *
 * Note the asymmetry with `dropUnknownWidgets`, which does not block a save:
 * those placements were named to the user and this build cannot render them
 * either way, whereas a withheld placement is one the server knows is there
 * and will hand back when the user's access returns.
 *
 * Errors propagate. A stale revision arrives as a `revision_conflict`, which
 * the caller surfaces as "this dashboard changed elsewhere" rather than
 * retrying — a retry is how two tabs overwrite each other.
 */
export async function saveLayout(
  scope: DashboardScope,
  config: DashboardLayoutConfig,
  signal?: AbortSignal,
): Promise<LayoutResponse> {
  // Scope first: until the observations are known to describe this scope, the
  // withheld list below is a statement about some other dashboard and the
  // revision is a claim about some other record. Refusing is also the right
  // answer for a save with no load behind it at all -- revision 0 against an
  // existing record is a conflict the server would reject anyway, and this
  // says so without the round trip.
  if (loadedScope !== scope) {
    throw new StaleLayoutScopeError(scope, loadedScope);
  }

  const withheld = layoutWithheld.value;
  if (withheld.length > 0) {
    throw new WithheldLayoutError(withheld);
  }

  const saved = await preferencesApi.saveLayout(
    scope,
    layoutRevision.value,
    config,
    signal,
  );

  // Before the signal writes, so a read that resolves after this point sees a
  // changed counter and discards itself rather than restoring the old revision.
  commitSeq += 1;
  layout.value = config;
  layoutRevision.value = saved.revision;
  layoutLoaded.value = true;
  layoutUnavailable.value = undefined;
  return saved;
}
