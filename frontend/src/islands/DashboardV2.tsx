import { useComputed, useSignal } from "@preact/signals";
import type { JSX } from "preact";
import { useEffect, useMemo, useRef } from "preact/hooks";
import DashboardGrid from "@/components/dashboard/DashboardGrid.tsx";
import EditToolbar from "@/components/dashboard/EditToolbar.tsx";
import LayoutCopyDialog from "@/components/dashboard/LayoutCopyDialog.tsx";
import WidgetPalette from "@/components/dashboard/WidgetPalette.tsx";
import WidgetParamDialog from "@/components/dashboard/WidgetParamDialog.tsx";
import { Alert } from "@/components/ui/Alert.tsx";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog.tsx";
import { Skeleton } from "@/components/ui/Skeleton.tsx";
// Registers every shipped widget before first render.
import "@/components/dashboard/widgets/index.ts";
import { selectedCluster } from "@/lib/cluster.ts";
import type { FamilyStatuses } from "@/lib/dashboard/catalog.ts";
import { dashboardData } from "@/lib/dashboard/data.ts";
import type { EditSession } from "@/lib/dashboard/edit-session.ts";
import {
  applyChange,
  beginEdit,
  commit,
  discard,
  isDirty,
} from "@/lib/dashboard/edit-session.ts";
import { resolveRenderable } from "@/lib/dashboard/grid.ts";
import type {
  CopyableLayout,
  LayoutUnavailable,
} from "@/lib/dashboard/layout-store.ts";
import {
  copyableLayoutRecords,
  copyableLayouts,
  defaultLayoutFor,
  layout,
  layoutGeneration,
  layoutLoaded,
  layoutRevision,
  layoutUnavailable,
  layoutWarnings,
  layoutWithheld,
  loadCopyableLayouts,
  loadLayout,
  StaleLayoutScopeError,
  saveLayout,
  WithheldLayoutError,
} from "@/lib/dashboard/layout-store.ts";
import { widgetSourceKeys } from "@/lib/dashboard/params.ts";
import { narrowParams, placeNewWidget } from "@/lib/dashboard/placement.ts";
import { getWidget } from "@/lib/dashboard/registry.ts";
import type {
  DashboardLayoutConfig,
  LayoutItem,
  WidgetDef,
} from "@/lib/dashboard/types.ts";
import { FAMILY_STATUS_KEYS } from "@/lib/dashboard/types.ts";
import { sourcesOf } from "@/lib/dashboard/widget-state.ts";
import type {
  ClusterInfoData,
  DashboardSummary,
} from "@/lib/dashboard/wire-types.ts";
import { useDashboardFocus } from "@/lib/hooks/use-dashboard-focus.ts";
import { preferenceReason } from "@/lib/preferences.ts";
import { showToast } from "@/src/islands/ToastProvider.tsx";
import { IS_BROWSER } from "@/src/lib/is-browser.ts";

/**
 * What Save's title says once a write has been refused.
 *
 * "Reloaded" is the literal instruction and not a euphemism: nothing short of
 * a fresh read lets the client claim a revision again, and leaving edit mode
 * is not a read. See `saveBlocked`.
 */
const SAVE_BLOCKED_REASON =
  "This dashboard has to be reloaded before it can be saved again.";

const TIME_RANGES = ["15m", "1h", "6h", "24h"] as const;
type TimeRange = (typeof TIME_RANGES)[number];

// Shared by the SSR placeholder and the hydrated root. Preact hydration keeps
// the server-rendered root's props, so the two must not diverge.
const ROOT_STYLE: JSX.CSSProperties = { minHeight: "400px" };

/**
 * Every source this layout's widgets read, plus the two the header reads for
 * its subtitle and the seven the catalog needs to answer whether a widget can
 * work on this cluster at all.
 *
 * Derived from the live layout rather than the shipped default: a stored
 * layout carrying a widget the default does not have would otherwise never
 * see its source fetched, and would sit in a loading state forever.
 */
function sourcesFor(config: DashboardLayoutConfig): string[] {
  return [
    ...new Set<string>([
      "cluster-info",
      "dashboard-summary",
      // Every family's discovery status, whether or not a widget reading it is
      // on this layout. The palette has to mark a widget that cannot work on
      // this cluster BEFORE it is added (R3), and a widget that is merely in
      // the catalog has nothing placed to pull its status in -- so the status
      // set is a property of the catalog, not of the arrangement. Seven extra
      // reads on mount, deduped by `ensure` and refreshed on the same 60s
      // tick as everything else.
      ...FAMILY_STATUS_KEYS,
      // `sourcesOf`, not `.sources`: a widget's declared family status is not
      // in its own source list, and a widget whose status is never fetched
      // sits in the skeleton forever.
      //
      // And `widgetSourceKeys` over the result, not the source names
      // themselves: a parameterized widget's read is cached under a key that
      // carries its values, so this has to ask for the same key `WidgetHost`
      // will resolve against. Two diagnostics widgets on different namespaces
      // therefore produce two requests, and two on the same namespace produce
      // one -- which is the Set here and the dedupe in `ensure` agreeing.
      ...config.items.flatMap((i) => {
        const def = getWidget(i.id);
        return def ? widgetSourceKeys(sourcesOf(def), i.params ?? {}) : [];
      }),
    ]),
  ];
}

/**
 * The reasons whose wording does not depend on whether the layout was being
 * read or written.
 *
 * Both copy helpers below consult this first. They used to carry byte-identical
 * sentences for these two reasons, three dozen lines apart, so a copy edit had
 * to be remembered twice and a half-applied one would tell the user two
 * different things about the same condition.
 *
 * `null` means "not one of the shared reasons" -- the caller then applies its
 * own read-specific or write-specific wording.
 */
function sharedReasonCopy(
  reason: LayoutUnavailable | undefined,
): string | null {
  switch (reason) {
    case "database_unavailable":
      return "Saved dashboard layouts need a database, and this deployment has none configured.";
    case "identity_too_long":
      return "Your account identity is longer than stored layouts support. An operator has to shorten the mapped identity attribute.";
    default:
      return null;
  }
}

/**
 * Human text for the reasons a layout load can fail with. Mirrors the same
 * helper in SavedViews.tsx, including its rule: a reason not named here gets
 * generic copy rather than invented specifics, because describing the wrong
 * failure is worse than admitting we cannot name this one.
 *
 * `"unknown"` — a dropped connection, a reason-less 500 — falls through to the
 * default, which is the honest thing to say about it. The type excludes
 * `undefined` so "nothing went wrong" cannot reach here at all.
 */
function unavailableCopy(reason: LayoutUnavailable): string {
  const shared = sharedReasonCopy(reason);
  if (shared !== null) return shared;
  switch (reason) {
    case "invalid_config":
    case "unsupported_schema_version":
    case "unknown_widget_id":
      return "Your stored layout no longer describes something this server can read.";
    default:
      return "Your saved layout could not be loaded.";
  }
}

/**
 * Human text for a save that did not happen. Same rule as `unavailableCopy`:
 * a failure this cannot name is reported as a failure rather than dressed in
 * the last message somebody wrote.
 *
 * `revision_conflict` is deliberately absent — it is not a message, it is a
 * choice, and it goes to the dialog below rather than a toast.
 */
function saveErrorCopy(err: unknown): string {
  // Raised before the request goes out, so no reason code exists for them.
  if (err instanceof WithheldLayoutError) {
    return "This layout cannot be saved while some of its widgets are hidden, which is what keeps them from being deleted.";
  }
  if (err instanceof StaleLayoutScopeError) {
    return "Your dashboard was reloaded while you were editing it. Reload the page and arrange it again.";
  }
  const reason = preferenceReason(err);
  const shared = sharedReasonCopy(reason);
  if (shared !== null) return shared;
  switch (reason) {
    case "limit_reached":
      return "This dashboard has more widgets than a saved layout can hold.";
    case "invalid_config":
    case "unknown_widget_id":
    case "unsupported_schema_version":
      return "This arrangement is not one the server can store.";
    default:
      return "Your layout could not be saved.";
  }
}

/**
 * The layout as the grid actually renders it.
 *
 * `resolveRenderable` is what the grid runs on the way in: it drops placements
 * this build has no widget for and re-compacts what is left. An edit session
 * opened over the unnormalized config would measure dirtiness against a layout
 * that is not on screen, and the first drag would then look like two changes.
 */
function asRendered(config: DashboardLayoutConfig): DashboardLayoutConfig {
  return {
    ...config,
    items: resolveRenderable(config.items, getWidget).map((r) => r.item),
  };
}

export default function DashboardV2() {
  const timeRange = useSignal<TimeRange>("1h");
  /**
   * The open edit session, or null when not editing.
   *
   * Edit mode is deliberately not persisted; the layout it produces is. The
   * session holds the baseline and the revision alongside the working copy so
   * a Save cannot write a stale revision — see edit-session.ts.
   */
  const session = useSignal<EditSession | null>(null);
  const saving = useSignal(false);
  /** The catalog palette. Only meaningful while a session is open. */
  const paletteOpen = useSignal(false);
  /** The copy dialog. Only meaningful while a session is open. */
  const copyOpen = useSignal(false);
  /**
   * The widget whose parameters are being collected, or null.
   *
   * `editing` distinguishes the two things one dialog does. Absent, the widget
   * has been chosen from the palette and nothing is on the layout yet --
   * cancelling places nothing. Present, it is a placed widget being re-pointed
   * at something else, and cancelling leaves it exactly as it was.
   *
   * Held here rather than in the palette or the grid because the values it
   * collects have to reach the open edit session, and both of those are
   * components that do not have one.
   */
  const paramTarget = useSignal<{
    def: WidgetDef;
    editing?: LayoutItem;
  } | null>(null);
  /** The Cancel confirmation, shown only when there is work to lose. */
  const confirmDiscard = useSignal(false);
  /**
   * The Reset confirmation.
   *
   * Behind a dialog although Reset, like every other gesture in the editor,
   * writes nothing until Save (D-6). The difference is what it discards: a
   * drag moves one widget and a removal takes one away, while this replaces
   * the whole arrangement at once -- including a layout the user saved months
   * ago and has not looked at since. Cancel would still put it back, but only
   * for as long as the session lasts, and the whole point of the button is
   * that the user is about to arrange something new on top of it.
   */
  const confirmReset = useSignal(false);
  /**
   * What the last gesture inside this session had to drop, in words.
   *
   * Kept apart from `layoutWarnings`, which the store owns and which describes
   * the last load. This one describes something the user just did, and clearing
   * it when they do the next thing is the point: a warning about a copy is
   * stale the moment the layout is replaced again.
   */
  const editWarnings = useSignal<string[]>([]);
  /** The save conflict, which is a choice rather than a message. */
  const conflict = useSignal(false);
  /**
   * Set once a save has been refused, and cleared only by a completed load.
   *
   * The store drops its observation whenever a write fails, because the write
   * may have committed on the way out and its revision would then be a lie
   * (layout-store.ts, `saveLayout`'s catch). Until a fresh read, no save may
   * claim a revision at all -- so a second attempt would be refused for a
   * different and much stranger reason than the first.
   *
   * Cleared by the load effect and NOT by opening another session, because
   * leaving edit mode and coming back reads nothing: the block would lift
   * while the refusal underneath it stood, which is the same broken Save
   * button with a longer path to it.
   */
  const saveBlocked = useSignal(false);
  /**
   * A replacement layout is being read and the grid has not adopted it yet.
   *
   * The first load withdraws Edit through `layoutPending`, but that signal is
   * `!layoutLoaded`, and the store sets `layoutLoaded` true once and never back
   * to false -- so every load after the first leaves Edit enabled while its GET
   * is outstanding. Taking the stored layout after a conflict is exactly such a
   * load, and a session opened in that window is erased without warning when
   * `adoptLoadedLayout` re-mounts the grid over it. Same destroyed-work case the
   * first-load gate exists to prevent, so it gets the same gate.
   */
  const reloading = useSignal(false);

  /**
   * What the grid mounts from, and the key that re-mounts it.
   *
   * The grid copies `initial` into its own signal once, on mount, because the
   * working copy is what the pointer sessions reshape — so handing it a new
   * starting point means re-mounting it. Three things do: a completed load, a
   * cancel putting the baseline back, and taking the stored layout after a
   * conflict.
   *
   * The epoch is bumped on every call, never conditionally on the config
   * having changed. What is being replaced is the grid's private working copy,
   * and that can differ from `gridSource` without `gridSource` differing from
   * anything — an edited dashboard whose reload comes back 204 hands the very
   * same default object straight back, and a guard comparing the two would
   * leave the discarded edits on screen.
   */
  const gridSource = useSignal<DashboardLayoutConfig>(layout.peek());
  const gridEpoch = useSignal(0);
  function mountGrid(config: DashboardLayoutConfig) {
    gridSource.value = config;
    gridEpoch.value += 1;
  }

  /**
   * The load generation the grid is already mounted on.
   *
   * A load bumps the store's generation and the effect below re-mounts on it.
   * The conflict path re-mounts by hand, because it has to work even for a
   * load that changes nothing the store can see; recording the generation it
   * mounted at is what stops the effect from mounting a second time over it.
   */
  const mountedGeneration = useRef(layoutGeneration.peek());

  const editing = session.value !== null;

  /**
   * Where the keyboard goes when this editor changes shape.
   *
   * Three buttons and three arming gestures used to be six refs and two layout
   * effects sitting in this island, and that cluster produced the two worst
   * defects of D16's review round: the pairings are individually simple and
   * only make sense read together. They are read together in one place now --
   * `lib/hooks/use-dashboard-focus.ts` -- and this island asks for an outcome
   * ("return to Edit when this closes") instead of setting a flag and
   * reasoning about which effect will notice it.
   *
   * It is handed edit mode and the grid epoch because those are the two
   * renders focus has to ride: the toolbar swaps its buttons on the first, and
   * the grid replaces every cell on the second.
   */
  const focus = useDashboardFocus(editing, gridEpoch.value);

  /**
   * The seven family statuses, as one value the palette can be handed.
   *
   * Computed rather than rebuilt inline on every render: `WidgetPalette`
   * memoizes its catalog rows on this, and a fresh object per render would
   * re-run a bounded grid scan per entry on every keystroke in its search box.
   * A computed changes identity only when one of the seven states actually
   * does.
   */
  const familyStatuses = useComputed<FamilyStatuses>(() =>
    Object.fromEntries(
      FAMILY_STATUS_KEYS.map((key) => [
        key,
        dashboardData.signalFor(key).value,
      ]),
    ),
  );

  // The layout on screen decides which sources are fetched, so this re-runs
  // when the load replaces the default with the user's arrangement.
  //
  // It runs first against the default, before that load has landed, and that
  // is the trade wanted: first paint does not wait on the layout round trip.
  // For the unsaved-dashboard case the two source sets are identical anyway.
  // For a customized one it can fetch a source only a default widget reads --
  // `ensure` is keyed per source and range, so the cost is those few requests
  // once, not a refetch of everything.
  //
  // The working copy takes precedence over the store's layout while a session
  // is open: a widget added from the palette is not in the stored layout and
  // never will be until the user saves, so keying this on the store alone
  // would leave whatever it reads unfetched and the new card in a loading
  // state that never resolves. `ensure` skips a source it has already fetched,
  // so re-running it on every drag costs nothing.
  //
  // Fetching the added widget's sources inside `addWidget` instead, and
  // leaving this keyed on the store, looks narrower and is wrong: a time-range
  // change later in the same session would re-ensure only the stored layout's
  // sources, so a range-sensitive source that only the added widget reads
  // would never be fetched for the new range, and that card alone would go on
  // showing the old one.
  useEffect(() => {
    if (!IS_BROWSER) return;
    const config = session.value?.working ?? layout.value;
    const keys = sourcesFor(config);
    dashboardData.ensure(keys, timeRange.value);
    // And forget everything else. This is the only place that can truthfully
    // say what the WHOLE set is -- `ensure` is told what a caller wants, not
    // what nobody wants any more -- and it matters only now that a key can
    // carry parameters: re-pointing a diagnostics widget from prod to staging
    // leaves prod recorded as fetched, and the refresh loop re-requests
    // everything it has ever fetched. Without this, an afternoon of
    // re-pointing one card leaves a namespace being polled every 60s per
    // abandoned value, in the backend's shared 30-request-per-minute bucket.
    //
    // Safe against the transient states this effect runs in, because `keys`
    // is always the superset in play: the working copy while a session is
    // open (which holds widgets the stored layout does not), the stored
    // layout otherwise, plus the header's two sources and all seven family
    // statuses unconditionally.
    dashboardData.retain(keys);
  }, [timeRange.value, layout.value, session.value]);

  // Mount only. A cluster switch reloads the page (ClusterSwitcher.tsx:239),
  // which is what re-reads the layout for the new cluster — layouts are
  // per-user, per-cluster, per-scope, so one that leaked across a switch would
  // be the wrong dashboard rather than a stale one.
  useEffect(() => {
    if (!IS_BROWSER) return;
    const ac = new AbortController();
    loadLayout("overview", ac.signal);
    return () => ac.abort();
  }, []);

  // A completed load is a new starting point for the grid.
  //
  // It cannot land on top of an open session: editing is withheld until the
  // first load settles (`editDisabled` below), and there is no second load —
  // a cluster switch reloads the page. The day a reload affordance ships, this
  // has to decide what happens to a session it would discard.
  useEffect(() => {
    if (!IS_BROWSER || layoutGeneration.value === mountedGeneration.current) {
      return;
    }
    adoptLoadedLayout();
  }, [layoutGeneration.value]);

  useEffect(() => {
    if (!IS_BROWSER) return;
    const stop = dashboardData.startRefresh();
    return () => {
      stop();
      dashboardData.abort();
    };
  }, []);

  /**
   * Puts the store's layout on the grid and records that it is there.
   *
   * Both callers -- the effect above and the conflict path -- have to do all
   * three things together: re-mount, note the generation so the other one does
   * not re-mount over it, and lift the save block, since a completed read is
   * exactly what the store needs before it can claim a revision again.
   */
  function adoptLoadedLayout() {
    mountedGeneration.current = layoutGeneration.peek();
    saveBlocked.value = false;
    mountGrid(layout.peek());
  }

  /** Opens a session over the layout on screen, at the revision it loaded at. */
  function startEditing() {
    focus.armToolbarFocus();
    // Never inherited from the last session: these are dialogs, and one that
    // reappears on its own the next time the user presses Edit is a dialog
    // nobody asked for. Warnings go with them -- they describe a gesture in
    // the session that just ended.
    paletteOpen.value = false;
    copyOpen.value = false;
    paramTarget.value = null;
    editWarnings.value = [];
    session.value = beginEdit(asRendered(layout.value), layoutRevision.value);
    // Read here rather than on mount, so the request is only made by someone
    // who might use it -- and early enough in the gesture that the answer is
    // there before the user has finished reading the toolbar. Not awaited:
    // nothing else in this function depends on it, and the affordance it feeds
    // simply appears when it lands. It settles its own failure (see the
    // store): a copy list that could not be read offers no copy button, which
    // is what a user with no other clusters gets anyway.
    void loadCopyableLayouts();
  }

  /**
   * Adds a widget from the catalog and puts the user on it.
   *
   * The grid owns its working copy and takes a new one only by re-mounting
   * (see `mountGrid`), so an insertion goes in through the same door a load
   * and a cancel do, and the session is told separately -- the grid does not
   * report the layout it mounts with, and a Save armed by everything except
   * Add would be worse than no Save at all.
   *
   * `asRendered` runs over the result for the same reason `startEditing` runs
   * it over the loaded layout: it is what the grid itself will do on mount, so
   * anything else recorded here would differ from what is on screen by a
   * compaction pass.
   *
   * `placeNewWidget` returning `null` means the layout has no room left for
   * this widget below `DASHBOARD_MAX_ROWS`. The palette itself already
   * disables an entry it cannot place, with a stated reason, so reaching this
   * function with an unplaceable widget means that guard was bypassed --
   * doing nothing here is the honest response, not a substitute toast the
   * user would have no context for.
   */
  function addWidget(def: WidgetDef) {
    const s = session.value;
    if (s === null || saving.value) return;
    // A widget that needs values is not placed yet. The palette closes, the
    // parameter dialog opens over the same session, and the placement happens
    // on confirm -- so cancelling leaves the layout untouched rather than
    // leaving a half-configured card on it (KTD3). Every other widget places
    // immediately, exactly as before.
    if (def.params !== undefined) {
      paletteOpen.value = false;
      paramTarget.value = { def };
      return;
    }
    placeWidget(def, {});
  }

  /**
   * Puts a widget on the working copy, with whatever values it carries.
   *
   * Split out of `addWidget` so the immediate path and the confirm path share
   * one placement, one session write and one re-mount. They differ only in
   * where the values came from.
   */
  function placeWidget(def: WidgetDef, params: Record<string, string>) {
    const s = session.value;
    if (s === null || saving.value) return;
    const placed = placeNewWidget(
      s.working.items,
      def,
      s.working.columns,
      params,
    );
    // No session write, no re-mount, no pending focus: `placeNewWidget`'s own
    // comments explain why a clamped or best-effort position is worse than
    // refusing outright, and this island has no information the placement
    // module did not already have when it decided there was nowhere to put
    // this widget.
    if (placed === null) return;
    const next = asRendered({
      ...s.working,
      items: [...s.working.items, placed],
    });
    session.value = applyChange(s, next.items);
    paletteOpen.value = false;
    paramTarget.value = null;
    focus.focusOnInsert(placed.instanceId);
    mountGrid(next);
  }

  /**
   * Takes the values the dialog collected: places a new widget, or re-points
   * a placed one.
   *
   * The re-point writes nothing but `params`. Position and size are copied
   * through untouched, which is the whole reason this affordance exists --
   * remove-and-re-add would repair the widget by rearranging the dashboard.
   * The re-mount is still needed: the grid owns its working copy and takes a
   * new one only by mounting (see `mountGrid`), and the card has to re-render
   * against the new namespace's cache entry.
   */
  function confirmParams(values: Record<string, string>) {
    const target = paramTarget.value;
    const s = session.value;
    if (target === null || s === null || saving.value) return;

    if (target.editing === undefined) {
      placeWidget(target.def, values);
      return;
    }

    const instanceId = target.editing.instanceId;
    // Matched by instanceId against the CURRENT working copy rather than
    // written over the snapshot the dialog was opened with: the snapshot
    // carries an x, y, w and h that were true when the dialog opened, and
    // only `params` is this gesture's to change.
    //
    // `narrowParams` for the same reason `placeNewWidget` applies it: a value
    // under a key the widget does not declare is refused outright by the
    // server, and would make the whole layout unsaveable over a field nothing
    // on screen reads.
    const next = asRendered({
      ...s.working,
      items: s.working.items.map((i) =>
        i.instanceId === instanceId
          ? { ...i, params: narrowParams(target.def, values) }
          : i,
      ),
    });
    session.value = applyChange(s, next.items);
    paramTarget.value = null;
    // The cell is being re-mounted, so this is armed rather than immediate --
    // the same door an insertion goes through, and the dialog the user was
    // standing in has gone.
    focus.focusOnInsert(instanceId);
    mountGrid(next);
  }

  /**
   * Closes the parameter dialog, placing and changing nothing.
   *
   * Where the keyboard goes depends on where it came from: re-pointing a
   * placed widget returns to that widget's cell, while adding one returns to
   * "Add widget", because the palette that was open when the dialog replaced
   * it is closed and there is no row to go back to.
   */
  function cancelParams() {
    const target = paramTarget.value;
    paramTarget.value = null;
    if (target?.editing !== undefined) {
      focus.focusPlacement(target.editing.instanceId);
      return;
    }
    focus.focusAddButton();
  }

  /** Opens the parameter dialog over a placed widget, pre-filled. */
  function reparameterize(item: LayoutItem) {
    const s = session.value;
    if (s === null || saving.value) return;
    const def = getWidget(item.id);
    // Unreachable through the control -- the grid renders it only for a
    // widget whose definition declares parameters, which it looked up to
    // render the card at all. Guarded because a definition is looked up by a
    // stored id, and doing nothing is the honest response to an id this build
    // has no widget for.
    if (def === undefined || def.params === undefined) return;
    paletteOpen.value = false;
    copyOpen.value = false;
    paramTarget.value = { def, editing: item };
  }

  /** Closes the palette and leaves the keyboard on the button that opened it. */
  function closePalette() {
    paletteOpen.value = false;
    focus.focusAddButton();
  }

  /** Closes the copy dialog and leaves the keyboard on its opener. */
  function closeCopyDialog() {
    copyOpen.value = false;
    focus.focusCopyButton();
  }

  /**
   * Replaces the working copy with the shipped default (D-6).
   *
   * The default, not the layout the user last saved. "Restore what I had" is
   * undo -- different state, different feature -- and a button that does
   * whichever of the two the reader guessed is worse than one that does the
   * narrower thing and says so.
   *
   * It goes through the session and the re-mount like every other replacement,
   * and it writes nothing: Save is still the only thing that reaches the
   * server. It does not force the session dirty either. `isDirty` compares the
   * working copy against the baseline, so resetting a dashboard that is
   * already the default leaves Save disabled -- which is correct, because
   * there would be nothing to write.
   */
  function resetLayout() {
    const s = session.value;
    if (s === null || saving.value) return;
    confirmReset.value = false;
    // `asRendered` for the same reason `startEditing` and `addWidget` run it:
    // it is what the grid does on mount, so anything else recorded here would
    // differ from what is on screen by a compaction pass. The scope's own
    // default, so a second dashboard gets its own rather than the overview's.
    const next = asRendered({
      ...s.working,
      items: defaultLayoutFor(s.working.scope).items,
    });
    session.value = applyChange(s, next.items);
    editWarnings.value = [];
    // The dialog the user was standing in has gone and the session is still
    // open, so nothing else on the way out restores the keyboard.
    focus.focusResetButton();
    mountGrid(next);
  }

  /**
   * Takes a layout the user arranged on another cluster (D-3).
   *
   * A replacement, not a merge: the rows offer whole dashboards, and silently
   * folding one into what is already on screen would produce an arrangement
   * neither cluster has. The items carry their source instanceIds, which is
   * safe because the whole list is replaced -- nothing they could collide
   * with survives.
   *
   * `scope` and `columns` stay the target's. `copyableLayouts` has already
   * refused any record that disagrees about either, so this cannot quietly
   * re-address a layout to the wrong dashboard or the wrong grid.
   *
   * Warnings ride along because the copy may be short: a widget this build
   * does not have, or a placement the server would not hand across clusters.
   * A dashboard that arrives missing two cards and says nothing reads as a
   * copy that lost them.
   */
  function copyFrom(entry: CopyableLayout) {
    const s = session.value;
    if (s === null || saving.value) return;
    const next = asRendered({ ...s.working, items: entry.config.items });
    session.value = applyChange(s, next.items);
    editWarnings.value = entry.warnings;
    copyOpen.value = false;
    focus.focusCopyButton();
    mountGrid(next);
  }

  /**
   * Records what the grid is now holding.
   *
   * Guarded on there being a session: the grid reports its restore on the way
   * out too, and a session already closed must not be reopened by it.
   */
  function handleChange(items: LayoutItem[]) {
    const s = session.value;
    if (s === null) return;
    session.value = applyChange(s, items);
  }

  /**
   * Closes the session and puts the grid back to the layout as loaded.
   *
   * Always a restore: this is the Cancel path, and the two exits that keep
   * what is on screen -- a successful save and taking the server's layout
   * after a conflict -- close the session themselves, because each has its own
   * idea of what the grid should be showing afterwards.
   */
  function closeEditing() {
    const s = session.value;
    if (s === null) return;
    confirmDiscard.value = false;
    confirmReset.value = false;
    paramTarget.value = null;
    editWarnings.value = [];
    session.value = null;
    focus.armReturnToEdit();
    mountGrid(discard(s));
  }

  /**
   * Leaves edit mode, asking first when there is work to lose.
   *
   * Leaving with unsaved changes is the one way to lose a layout silently, and
   * a dashboard is easy to click away from.
   */
  function requestExit() {
    const s = session.value;
    if (s === null || saving.value) return;
    if (!isDirty(s)) {
      closeEditing();
      return;
    }
    confirmDiscard.value = true;
  }

  async function save() {
    const s = session.value;
    if (s === null || saving.value || !isDirty(s)) return;
    const { config, revision } = commit(s);

    // The session pinned a revision when it opened; the store's is the one the
    // write will actually claim. They can only differ if a load landed while
    // editing, and saving then would overwrite a layout this session never
    // saw. Unreachable today (see the load effect above), and checked anyway:
    // the whole point of the session carrying a revision is that nothing else
    // gets to choose it.
    if (revision !== layoutRevision.value) {
      saveBlocked.value = true;
      conflict.value = true;
      return;
    }

    saving.value = true;
    try {
      await saveLayout("overview", config);
      // The store now holds exactly what the grid is showing, and deliberately
      // does not bump the generation, so the grid is not re-mounted: it is
      // already displaying the saved arrangement.
      session.value = null;
      paramTarget.value = null;
      // What the arrangement dropped on its way here is now what was saved,
      // and the user has been told once already. Keeping it would leave a
      // warning about a copy standing over a dashboard that is now stored.
      editWarnings.value = [];
      focus.armReturnToEdit();
      showToast("Dashboard layout saved", "success");
    } catch (err) {
      // Not a message but a choice, so it goes to the dialog rather than a
      // toast. The session stays open either way: the user's arrangement is
      // the only copy of this work that exists.
      saveBlocked.value = true;
      if (preferenceReason(err) === "revision_conflict") {
        conflict.value = true;
        return;
      }
      showToast(saveErrorCopy(err), "error");
    } finally {
      saving.value = false;
    }
  }

  /** Takes the stored layout and drops this session's edits with it. */
  async function reloadStoredLayout() {
    conflict.value = false;
    session.value = null;
    paramTarget.value = null;
    editWarnings.value = [];
    focus.armReturnToEdit();
    // No local restore: this is the one exit where the baseline is known to be
    // out of date, so what goes on screen has to come from the server.
    //
    // `loadLayout` settles rather than rejects, so this re-mounts either way:
    // on what the other tab saved, or -- if the read failed -- on the layout
    // the store was left holding. Either beats leaving the edits the user just
    // asked to drop sitting on the grid. The generation is recorded first so
    // the effect above does not re-mount a second time over this one.
    //
    // Edit is withheld across the whole await: the session is already closed,
    // so without this the user could reopen one, arrange it, and have it
    // re-mounted away the moment the response lands.
    reloading.value = true;
    try {
      await loadLayout("overview");
      adoptLoadedLayout();
    } finally {
      reloading.value = false;
    }
  }

  if (!IS_BROWSER) {
    return <div style={ROOT_STYLE} />;
  }

  const info = dashboardData.state<ClusterInfoData>("cluster-info");
  const summary = dashboardData.state<DashboardSummary>("dashboard-summary");
  // Wait for both to settle, so the subtitle never shows "0 nodes" for the
  // moment between one response and the other.
  const headerReady = [info, summary].every(
    (st) => st.data !== null || st.error !== null,
  );
  const nodeCount = summary.data?.nodes.total ?? info.data?.nodeCount ?? 0;
  const podCount = summary.data?.pods.total ?? 0;
  const clusterName = info.data?.platform ?? info.data?.clusterID ?? "cluster";

  // A load that failed leaves `layout` on the default. Saying so matters: an
  // unexplained default dashboard reads as "my arrangement was deleted".
  const unavailable = layoutUnavailable.value;
  const storageDown = unavailable !== undefined;
  const unavailableMessage = storageDown ? unavailableCopy(unavailable) : null;
  const withheldCount = layoutWithheld.value.length;
  // The stored layout has not arrived yet. Adopting it re-mounts the grid,
  // which throws away whatever the grid's private working copy holds -- so
  // until it lands there must be nothing in that copy worth keeping.
  const layoutPending = !layoutLoaded.value;
  // Withdrawn in two states, for two different reasons.
  //
  // Storage down: arranging a dashboard that cannot be stored is work the user
  // loses on the next reload, so the affordance is withdrawn rather than
  // offered and then disappointed.
  //
  // Load pending: what is on screen is still the default, and adopting the
  // stored layout re-mounts the grid. Editing in that window means a drag that
  // vanishes the instant the response lands, with no warning -- the one case
  // where offering the affordance actively destroys work rather than merely
  // wasting it.
  //
  // Withheld placements do NOT disable it, although they also block a save:
  // there the layout on screen is the user's own and the block is transient,
  // so editing it for this session is still worth something. The save path
  // reports the refusal, and the banner below has already explained it.
  //
  // Reloading is the same case as a pending first load, and is tracked
  // separately because `layoutLoaded` never returns to false once set.
  const editDisabled = storageDown || layoutPending || reloading.value;

  // What the copy affordance has to offer.
  //
  // The target's scope and grid decide which records are takeable, and both
  // live on the layout being edited rather than on a constant -- so this is
  // derived from the session, not from the catalog. Empty outside a session,
  // and empty while the list is still being read, which is what keeps the
  // button absent until there is something behind it.
  //
  // Memoized on the four things that can change the answer, and deliberately
  // NOT on the session itself. The session is a new object on every committed
  // change, so keying on it would re-scan every record and re-run
  // `dropUnknownWidgets` over every placement on each frame of a drag -- to
  // produce the same list, because moving a widget cannot change which of
  // another cluster's layouts may be copied. Same reason the palette memoizes
  // its catalog on `placed` rather than on the session.
  const copyScope = session.value?.working.scope;
  const copyColumns = session.value?.working.columns;
  const cluster = selectedCluster.value;
  const records = copyableLayoutRecords.value;
  const copyOptions = useMemo(
    () =>
      copyScope === undefined || copyColumns === undefined
        ? []
        : copyableLayouts(records, copyScope, cluster, copyColumns),
    [records, copyScope, cluster, copyColumns],
  );

  // The copy dialog's render gate includes "there is something to offer", so
  // the list emptying takes the dialog off screen. That is the right outcome
  // -- an empty list is nothing to choose from -- but it is not a close, and
  // without this the user is left with no dialog and no focus.
  //
  // It is reachable in one page load. `startEditing` clears `copyOpen` on
  // every Edit press but not the module-level record list, and fires the read
  // for the new session without awaiting it, so a second session can open the
  // dialog on the first session's answer and then have the new one land
  // narrower -- a layout deleted from another tab or device is enough.
  //
  // `focusAddButton`, not `focusCopyButton`: the opener is gated on the same
  // `copyOptions.length > 0` as the dialog, so it unmounts in this very
  // render and its ref is already null. "Add widget" is mounted for the whole
  // session, which is why `closePalette` leans on it too.
  useEffect(() => {
    if (!IS_BROWSER || !copyOpen.value || copyOptions.length > 0) return;
    copyOpen.value = false;
    focus.focusAddButton();
  }, [copyOptions.length, copyOpen.value]);

  return (
    <div style={ROOT_STYLE}>
      <div
        style={{
          display: "flex",
          alignItems: "flex-start",
          justifyContent: "space-between",
          marginBottom: "20px",
          gap: "16px",
          flexWrap: "wrap",
        }}
      >
        <div>
          <h1
            style={{
              margin: 0,
              fontSize: "24px",
              fontWeight: 700,
              letterSpacing: "-0.02em",
              color: "var(--text-primary)",
            }}
          >
            Cluster Overview
          </h1>
          {headerReady ? (
            <div
              style={{
                fontSize: "13px",
                color: "var(--text-muted)",
                marginTop: "4px",
              }}
            >
              {[
                clusterName,
                `${nodeCount} node${nodeCount !== 1 ? "s" : ""}`,
                `${podCount} pods`,
              ].join(" · ")}
            </div>
          ) : (
            <Skeleton class="h-4 w-80 mt-1" />
          )}
        </div>

        {/* Wraps, because edit mode is the widest this row ever gets and D17
            made it wider still: five buttons plus the four time ranges do not
            fit beside a title on a laptop split down the middle, and a row
            that cannot wrap pushes the time ranges off the page instead. The
            outer row already wraps the title away from these controls; this
            is the same rule one level in. */}
        <div
          style={{
            display: "flex",
            alignItems: "center",
            gap: "8px",
            flexWrap: "wrap",
            justifyContent: "flex-end",
          }}
        >
          <EditToolbar
            editing={editing}
            dirty={session.value !== null && isDirty(session.value)}
            saving={saving.value}
            saveBlockedReason={
              saveBlocked.value ? SAVE_BLOCKED_REASON : undefined
            }
            disabled={editDisabled}
            disabledReason={
              storageDown
                ? (unavailableMessage ?? undefined)
                : layoutPending || reloading.value
                  ? "Loading your saved layout..."
                  : undefined
            }
            editButtonRef={focus.editButton}
            cancelButtonRef={focus.cancelButton}
            addButtonRef={focus.addButton}
            copyButtonRef={focus.copyButton}
            resetButtonRef={focus.resetButton}
            paletteOpen={paletteOpen.value}
            copyAvailable={copyOptions.length > 0}
            copyOpen={copyOpen.value}
            onEdit={startEditing}
            onAddWidget={() => {
              paletteOpen.value = true;
            }}
            onCopyFromCluster={() => {
              copyOpen.value = true;
            }}
            onReset={() => {
              confirmReset.value = true;
            }}
            onCancel={requestExit}
            onSave={save}
          />

          <div
            style={{
              display: "flex",
              gap: "2px",
              background: "var(--glass-surface)",
              border: "1px solid var(--glass-border)",
              borderRadius: "8px",
              padding: "3px",
            }}
          >
            {TIME_RANGES.map((r) => (
              <button
                key={r}
                type="button"
                onClick={() => {
                  timeRange.value = r;
                }}
                style={{
                  padding: "5px 12px",
                  borderRadius: "6px",
                  border: "none",
                  cursor: "pointer",
                  fontSize: "12px",
                  fontWeight: 500,
                  background:
                    timeRange.value === r ? "var(--accent)" : "transparent",
                  color:
                    timeRange.value === r
                      ? "var(--bg-base)"
                      : "var(--text-muted)",
                  transition: "background 0.15s, color 0.15s",
                }}
              >
                {r}
              </button>
            ))}
          </div>
        </div>
      </div>

      {unavailableMessage !== null && (
        <Alert variant="warning" class="mb-4">
          {unavailableMessage} Showing the default dashboard.
        </Alert>
      )}

      {layoutWarnings.value.length > 0 && (
        <Alert variant="warning" class="mb-4">
          <ul style={{ margin: 0, paddingLeft: "18px" }}>
            {layoutWarnings.value.map((w) => (
              <li key={w}>{w}</li>
            ))}
          </ul>
        </Alert>
      )}

      {/* What the gesture the user just made had to drop -- today, a copy that
          arrived short. Separate from the banner above, which describes the
          last load: the two can be true at once and say different things, and
          merging them would make a warning about a layout the user took read
          as one about the layout they already had. */}
      {editWarnings.value.length > 0 && (
        <Alert variant="warning" class="mb-4">
          <ul style={{ margin: 0, paddingLeft: "18px" }}>
            {editWarnings.value.map((w) => (
              <li key={w}>{w}</li>
            ))}
          </ul>
        </Alert>
      )}

      {/*
        Unreachable with today's catalog -- every shipped widget declares no
        parameters and the server refuses parameters on such a widget, so no
        placement has a namespace to re-authorize. It is here because the
        refusal in saveLayout is, and a refusal the user cannot see is a save
        button that stops working for no stated reason.
      */}
      {withheldCount > 0 && (
        <Alert variant="warning" class="mb-4">
          {withheldCount === 1
            ? "One widget is hidden because you no longer have access to the namespace it reads."
            : `${withheldCount} widgets are hidden because you no longer have access to the namespaces they read.`}{" "}
          This dashboard cannot be saved until that access is restored, which is
          what keeps the hidden {withheldCount === 1 ? "widget" : "widgets"}{" "}
          from being deleted.
        </Alert>
      )}

      <DashboardGrid
        key={`layout-${gridEpoch.value}`}
        initial={gridSource.value}
        // Frozen while the write is in flight, not merely while editing.
        //
        // `commit` snapshots the payload before the await, so a drag or a
        // keyboard nudge landing during the round trip reaches the session but
        // never the server -- and the success path then clears the session and
        // reports "saved" over an arrangement that was not written. The user
        // keeps looking at the newer layout, cannot re-save it, and loses it on
        // the next reload. Cancel and Save are already held for the same
        // reason; the grid is the third control that had to be.
        //
        // The grid's own `[editable, narrow.value]` effect ends any pointer
        // session in flight with a restore, so the screen lands on exactly what
        // was written rather than on a half-finished gesture.
        editable={editing && !saving.value}
        onChange={handleChange}
        // The grid reports where a removal left a gap; the focus hook decides
        // what to do about it, including the case the grid cannot answer --
        // the widget that went was the last one, and the only place left to
        // put the keyboard is a toolbar button the grid does not own.
        onRemoved={focus.focusAfterRemoval}
        // The grid renders the control only for a widget that declares
        // parameters and reports the gesture; collecting the values and
        // deciding what the layout becomes is this island's job, the same
        // division `onRemoved` already follows.
        onReparameterize={reparameterize}
        onExitEdit={requestExit}
      />

      {/*
        Gated on the session as well as on its own flag: every exit from edit
        mode -- Cancel, a successful save, taking the server's layout after a
        conflict -- has to take the palette with it, and one condition that
        cannot be forgotten in a new exit path is worth more than a reset in
        each of them.

        And on the save not being in flight, which is the rule the grid and
        both toolbar exits already follow: whatever owns the layout while a
        request is out withdraws the editing surface for exactly that window.
        Unreachable through the pointer -- the scrim covers Save, and Tab is
        held inside the dialog -- so this is the invariant made structural
        rather than a case anyone has to keep arguing about.
      */}
      {paletteOpen.value && session.value !== null && !saving.value && (
        <WidgetPalette
          scope={session.value.working.scope}
          placed={session.value.working.items}
          columns={session.value.working.columns}
          familyStatuses={familyStatuses.value}
          onAdd={addWidget}
          onClose={closePalette}
        />
      )}

      {/* Gated on the session, on the save not being in flight, and on there
          being something to offer -- the same three conditions the palette
          carries, plus the one that is this dialog's own: a background read
          that came back empty must take the dialog with it rather than leave
          an empty list on screen. */}
      {copyOpen.value &&
        session.value !== null &&
        !saving.value &&
        copyOptions.length > 0 && (
          <LayoutCopyDialog
            layouts={copyOptions}
            onCopy={copyFrom}
            onClose={closeCopyDialog}
          />
        )}

      {/* The same three gates the palette carries -- its own flag, an open
          session, and no save in flight -- for the same reasons. A dialog
          collecting values for a layout that is being written, or for a
          session that has closed underneath it, would confirm into nothing. */}
      {paramTarget.value !== null &&
        session.value !== null &&
        !saving.value && (
          <WidgetParamDialog
            def={paramTarget.value.def}
            placed={session.value.working.items}
            editing={paramTarget.value.editing}
            onConfirm={confirmParams}
            onCancel={cancelParams}
          />
        )}

      {confirmReset.value && (
        <ConfirmDialog
          title="Reset dashboard layout"
          message="This replaces the arrangement on screen with the dashboard as it ships. Nothing is written until you press Save, and Cancel still puts back the layout you last saved."
          confirmLabel="Reset layout"
          danger
          onConfirm={resetLayout}
          onCancel={() => {
            confirmReset.value = false;
            focus.focusResetButton();
          }}
        />
      )}

      {confirmDiscard.value && (
        <ConfirmDialog
          title="Discard unsaved changes?"
          message="This dashboard goes back to the arrangement you last saved. There is no undo."
          confirmLabel="Discard changes"
          danger
          onConfirm={closeEditing}
          onCancel={() => {
            confirmDiscard.value = false;
          }}
        />
      )}

      {conflict.value && (
        <ConfirmDialog
          title="This dashboard changed somewhere else"
          message="Your layout was saved from another tab or device after you started editing. Loading it discards the changes you made here; keeping them lets you carry on, but this dashboard cannot be saved until you load the newer one."
          confirmLabel="Load the saved layout"
          onConfirm={() => {
            void reloadStoredLayout();
          }}
          // Escape and the scrim land here too, which is the safe default: the
          // arrangement on screen is the only copy of this session's work.
          onCancel={() => {
            conflict.value = false;
          }}
        />
      )}
    </div>
  );
}
