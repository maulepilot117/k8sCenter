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
import { selectedCluster } from "@/lib/cluster.ts";
import {
  type LayoutResponse,
  type PreferenceReason,
  preferenceReason,
  preferencesApi,
} from "@/lib/preferences.ts";
import { DEFAULT_OVERVIEW_LAYOUT } from "./default-layout.ts";
import { missingParamKeys } from "./params.ts";
import { getWidget } from "./registry.ts";
import type {
  DashboardLayoutConfig,
  DashboardScope,
  LayoutItem,
} from "./types.ts";
import { DASHBOARD_LAYOUT_SCHEMA_VERSION } from "./types.ts";

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
 * The shipped arrangement for one scope: what an unsaved dashboard renders,
 * and what Reset restores (D-6).
 *
 * Exported as a function rather than the map, so a caller cannot reach a scope
 * it did not ask for -- and so the `Record` above stays the one place a new
 * scope has to be given a default before it compiles.
 *
 * The returned object is the module constant itself, not a copy. Nothing in
 * the editor mutates a config in place -- `beginEdit` and `applyChange` clone,
 * and the grid's geometry engine rebuilds items rather than reshaping them --
 * so a copy here would cost a clone per Reset to defend against a rule the
 * whole feature already keeps. `loadLayout` hands the same object out for the
 * same reason on a 204.
 */
export function defaultLayoutFor(scope: DashboardScope): DashboardLayoutConfig {
  return DEFAULT_LAYOUTS[scope];
}

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
  const unfilled = new Set<string>();

  for (const it of config.items) {
    const def = getWidget(it.id);
    if (def === undefined) {
      missing.add(it.id);
      continue;
    }
    // The client twin of the server's rule that a declared parameter must
    // carry a value. Without it a placement stored before that rule existed
    // loads, renders a card that can never resolve, and then fails every
    // subsequent save of the whole layout with a message that names no
    // widget -- leaving the user a dashboard they cannot save and no way to
    // tell which of up to forty cards is at fault.
    //
    // Dropped rather than repaired, for the reason an unknown id is dropped:
    // there is no value to repair it with, and a notice naming the widget is
    // the recovery path.
    if (missingParamKeys(def.params, it.params ?? {}).length > 0) {
      unfilled.add(it.id);
      continue;
    }
    kept.push(it);
  }

  const warnings = [
    ...[...missing].map(
      (id) =>
        `Removed "${id}" from this dashboard: this build has no such widget.`,
    ),
    ...[...unfilled].map(
      (id) =>
        `Removed "${id}" from this dashboard: it was stored without the values it needs.`,
    ),
  ];

  // Dropping everything yields an empty layout, never the shipped default.
  // Substituting the default would discard an arrangement the user spent time
  // on because one build was missing its widgets, and the next save would
  // write that substitution over their work.
  return { config: { ...config, items: kept }, warnings };
}

/** One layout the user has on another cluster, ready to be taken. */
export interface CopyableLayout {
  /** The source record's id. Unique across clusters, so it keys the list. */
  id: string;
  /** The cluster it is stored on. What `data-cluster-id` carries. */
  clusterId: string;
  /**
   * What to call that cluster on screen.
   *
   * The server's label when the registry still names the cluster, and the raw
   * id when it does not -- a deregistered cluster has no name left to give,
   * and the id is the only honest thing to show for it. Resolved here rather
   * than in the dialog so the fallback is decided in the one module this
   * repo can unit test (D-10).
   */
  clusterLabel: string;
  /** When it was last saved there, so two clusters are told apart by age. */
  updatedAt: string;
  /** The arrangement to take, already stripped of what this build cannot
   * render. Never aliases the record it came from. */
  config: DashboardLayoutConfig;
  /** What taking it would silently lose, in words a user can act on. */
  warnings: string[];
}

/**
 * The layouts a user could copy onto the dashboard they are editing.
 *
 * The read half of D-3: layouts are scoped per (user, cluster, scope) so a
 * production dashboard can differ from a sandbox one, and the price of that is
 * arranging the same dashboard twice. This turns the list endpoint's answer
 * into the rows the editor offers.
 *
 * Pure, and here rather than in the dialog, because every rule below is a
 * judgement about which layouts are safe to take -- and the dialog is a
 * component, which this repo has no harness to test (D-10).
 *
 * Order is the endpoint's own: most recently updated first. A user looking for
 * the layout they just arranged elsewhere wants it at the top, and re-sorting
 * by cluster name would bury it.
 *
 * `columns` is the grid of the layout being edited, not the constant: a
 * placement is addressed to a column count, and one laid out on a different
 * grid describes cells the target does not have.
 */
export function copyableLayouts(
  records: readonly LayoutResponse[],
  scope: DashboardScope,
  currentCluster: string,
  columns: number,
): CopyableLayout[] {
  const out: CopyableLayout[] = [];

  for (const rec of records) {
    // The layout on screen is not a thing to copy from.
    if (rec.clusterId === currentCluster) continue;

    const cfg = rec.config;
    // The wire is untrusted here in a way the scoped read is not: that one
    // feeds a single typed config into one consumer, while this walks a list
    // whose every element the server assembled from a different row. A record
    // whose config is not a layout is skipped rather than thrown on -- one bad
    // row must not take the other clusters' offers down with it.
    if (cfg === null || typeof cfg !== "object" || !Array.isArray(cfg.items)) {
      continue;
    }
    // A different dashboard entirely.
    if (cfg.scope !== scope) continue;
    // Not a layout this build can read. `schemaVersion` is the reachable half
    // -- a newer build that bumps it will store layouts this one must decline
    // rather than misread -- and `columns` rides along because it is the same
    // question about geometry rather than shape: the server pins it to the one
    // grid it serves, so today only a future server could disagree. Taking
    // either would produce a save this build cannot explain being refused.
    if (
      cfg.schemaVersion !== DASHBOARD_LAYOUT_SCHEMA_VERSION ||
      cfg.columns !== columns
    ) {
      continue;
    }

    const { config, warnings } = dropUnknownWidgets(cfg);
    // Nothing left to take. Offering it would replace the dashboard with an
    // empty grid and an explanation, which is a worse outcome than the row not
    // being there.
    if (config.items.length === 0) continue;

    // The server drops placements naming a namespace out of a cross-cluster
    // listing, because it cannot re-authorize them against the cluster they
    // live on (HandleListLayouts). Unreachable today -- no shipped widget
    // takes parameters -- and said anyway, because a layout that arrives short
    // two widgets and says nothing looks like a copy that lost them.
    const withheld = rec.withheld ?? [];
    if (withheld.length > 0) {
      warnings.push(
        `${withheld.length} widget${withheld.length === 1 ? "" : "s"} on ` +
          `"${rec.clusterLabel || rec.clusterId}" read a namespace and ` +
          `cannot be copied to another cluster.`,
      );
    }

    out.push({
      id: rec.id,
      clusterId: rec.clusterId,
      clusterLabel: rec.clusterLabel || rec.clusterId,
      updatedAt: rec.updatedAt,
      // A fresh array, so the rows the dialog is still rendering and the
      // session the island is about to build cannot reach the same items.
      config: { ...config, items: [...config.items] },
      warnings,
    });
  }

  return out;
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
      config: defaultLayoutFor(scope),
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

/**
 * What the user has on their other clusters, as of the last successful read.
 *
 * Starts empty, which reads as "nothing to copy" -- the same thing a failed
 * read leaves behind, and deliberately so. This drives an optional affordance,
 * not a statement about the world: an editor that could not reach the list
 * offers no copy button, which is exactly what it offers for a user who has
 * layouts on no other cluster. Neither owes an explanation, because neither
 * takes anything away.
 *
 * That is the opposite of `layoutUnavailable`'s rule, and for the opposite
 * reason: there, an absent observation dressed as an empty one would show a
 * default dashboard as though the user's arrangement were gone.
 */
export const copyableLayoutRecords = signal<LayoutResponse[]>([]);

let copyableInFlight: AbortController | null = null;

/**
 * Reads the caller's layouts on every cluster, for the copy affordance.
 *
 * Separate from `loadLayout` and deliberately quiet: it does not settle
 * `layoutLoaded`, does not touch `layoutUnavailable`, and swallows its own
 * failure. Nothing on the dashboard depends on the answer, so a failed read
 * must not gate editing or raise a banner about a feature the user has not
 * reached for.
 *
 * Called when an edit session opens rather than on mount, so the request is
 * only made by someone who might use it -- and early enough in the gesture
 * that the answer is there before the user has read the toolbar.
 */
export async function loadCopyableLayouts(signal?: AbortSignal): Promise<void> {
  if (signal?.aborted) return;

  copyableInFlight?.abort();
  const ac = new AbortController();
  copyableInFlight = ac;

  const onAbort = () => ac.abort();
  signal?.addEventListener("abort", onAbort, { once: true });

  try {
    const records = await preferencesApi.listLayouts(ac.signal);
    if (ac.signal.aborted) return;
    copyableLayoutRecords.value = records;
  } catch {
    // Left as it was rather than cleared. A read that failed learned nothing,
    // and dropping a list the user is mid-way through reading would close a
    // dialog under them to report an error about a background refresh.
  } finally {
    signal?.removeEventListener("abort", onAbort);
    if (copyableInFlight === ac) copyableInFlight = null;
  }
}

let inFlight: AbortController | null = null;

/**
 * What the signals above currently describe, or null when they describe
 * nothing a save may rely on.
 *
 * A stored layout is keyed by (owner, cluster, scope). `layoutRevision` and
 * `layoutWithheld` are the two things a save reads and neither carries any of
 * those three, so without this a save would claim one key's revision and
 * consult another key's withheld list — and that list is the only thing
 * standing between a revoked namespace and a permanent delete. Owner is not
 * tracked here because the client never holds two identities at once; a
 * sign-out navigates and tears the module down.
 *
 * Cleared when a read or a write FAILS, and deliberately NOT while one is
 * merely in flight. A failed read teaches nothing, so the withheld list it
 * left behind is a statement about a world that may have moved — that is the
 * case worth refusing. A read still in flight is different: the previous
 * observation is the best evidence there is, the revision under it is a real
 * one the server checks, and refusing there would reject a save the user
 * legitimately asked for while a background refresh happened to be running.
 */
interface LayoutObservation {
  scope: DashboardScope;
  cluster: string;
}

let observed: LayoutObservation | null = null;

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

/** Raised instead of saving state that describes a different layout. */
export class StaleLayoutScopeError extends Error {
  constructor(
    public readonly requestedScope: DashboardScope,
    public readonly requestedCluster: string,
    public readonly observed: LayoutObservation | null,
  ) {
    super(
      `refusing to save scope "${requestedScope}" on cluster ` +
        `"${requestedCluster}": the store describes ` +
        (observed === null
          ? "no completed load"
          : `scope "${observed.scope}" on cluster "${observed.cluster}"`) +
        ", so its revision and withheld list do not describe what is being " +
        "written",
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
  //
  // This returns WITHOUT settling anything: no request was made, so there is
  // nothing to report. `layoutLoaded` therefore stays as it was, which for a
  // first load means the editor stays gated. That is correct for the only
  // caller shape that exists -- an island aborting on unmount, which is gone --
  // but a future caller that aborts while STAYING mounted must issue a
  // replacement load, or it will wait on a load that was never made.
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

    observed = { scope, cluster: selectedCluster.peek() };
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
    // The same staleness check the success path makes, and for the same
    // reason: a save committed while this read was in flight, so this read's
    // failure describes a state the write already moved past. Without it a
    // read that errors after a successful save tells the user their layout
    // could not be loaded and disables the editor, seconds after they saved it.
    if (commitSeq !== startedAt) return;

    layoutUnavailable.value = preferenceReason(err) ?? "unknown";
    // A failure settles the load: the caller has its answer, which is that
    // there isn't one. Leaving this false would make "still loading" and
    // "asked, and could not be told" the same state to every consumer.
    layoutLoaded.value = true;
    // layout.value is intentionally untouched: it is either the default the
    // module started at or the last layout that did load, and both are better
    // than an empty dashboard. The observation, though, is dropped: a read
    // that failed learned nothing, so the withheld list still in the signals
    // describes a world that may have moved on, and no save may rely on it.
    observed = null;
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
  // Identity first: until the store's observations are known to describe this
  // exact layout, the withheld list below is a statement about some other
  // dashboard and the revision is a claim about some other record. Refusing is
  // also the right answer for a save with no completed load behind it --
  // revision 0 against an existing record is a conflict the server would
  // reject anyway, and this says so without the round trip.
  const cluster = selectedCluster.peek();
  if (
    observed === null ||
    observed.scope !== scope ||
    observed.cluster !== cluster
  ) {
    throw new StaleLayoutScopeError(scope, cluster, observed);
  }

  const withheld = layoutWithheld.value;
  if (withheld.length > 0) {
    throw new WithheldLayoutError(withheld);
  }

  let saved: LayoutResponse;
  try {
    saved = await preferencesApi.saveLayout(
      scope,
      layoutRevision.value,
      config,
      signal,
    );
  } catch (err) {
    // The request may have committed server-side and failed on the way back --
    // an abort after the bytes went out, or a truncated success body. The
    // stored revision would then be one ahead of ours, and a retry reusing
    // layoutRevision would be refused as a conflict the user did not cause.
    // Dropping the observation forces a fresh read before any further save.
    observed = null;
    throw err;
  }

  // Before the signal writes, so a read that resolves after this point sees a
  // changed counter and discards itself rather than restoring the old revision.
  commitSeq += 1;
  // The write is now what the store describes, so the observation is restored
  // to this identity rather than left cleared by any load that raced it.
  observed = { scope, cluster };
  layout.value = config;
  layoutRevision.value = saved.revision;
  layoutLoaded.value = true;
  layoutUnavailable.value = undefined;
  return saved;
}
