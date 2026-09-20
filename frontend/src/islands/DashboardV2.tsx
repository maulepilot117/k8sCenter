import { useSignal } from "@preact/signals";
import type { JSX } from "preact";
import { useEffect, useLayoutEffect, useRef } from "preact/hooks";
import DashboardGrid from "@/components/dashboard/DashboardGrid.tsx";
import EditToolbar from "@/components/dashboard/EditToolbar.tsx";
import { Alert } from "@/components/ui/Alert.tsx";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog.tsx";
import { Skeleton } from "@/components/ui/Skeleton.tsx";
// Registers every shipped widget before first render.
import "@/components/dashboard/widgets/index.ts";
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
import type { LayoutUnavailable } from "@/lib/dashboard/layout-store.ts";
import {
  layout,
  layoutGeneration,
  layoutLoaded,
  layoutRevision,
  layoutUnavailable,
  layoutWarnings,
  layoutWithheld,
  loadLayout,
  StaleLayoutScopeError,
  saveLayout,
  WithheldLayoutError,
} from "@/lib/dashboard/layout-store.ts";
import { getWidget } from "@/lib/dashboard/registry.ts";
import type {
  DashboardLayoutConfig,
  DataSourceKey,
  LayoutItem,
} from "@/lib/dashboard/types.ts";
import type {
  ClusterInfoData,
  DashboardSummary,
} from "@/lib/dashboard/wire-types.ts";
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
 * its subtitle.
 *
 * Derived from the live layout rather than the shipped default: a stored
 * layout carrying a widget the default does not have would otherwise never
 * see its source fetched, and would sit in a loading state forever.
 */
function sourcesFor(config: DashboardLayoutConfig): DataSourceKey[] {
  return [
    ...new Set<DataSourceKey>([
      "cluster-info",
      "dashboard-summary",
      ...config.items.flatMap((i) => getWidget(i.id)?.sources ?? []),
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
  /** The Cancel confirmation, shown only when there is work to lose. */
  const confirmDiscard = useSignal(false);
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

  // Escape on a focused widget leaves edit mode, which takes that widget out
  // of the tab order under the focus that is on it. Focus has to land
  // somewhere deliberate, and where editing started is the only place the user
  // asked for. The Cancel and Save buttons need it too: both unmount the
  // moment the session closes.
  const editButton = useRef<HTMLButtonElement | null>(null);
  const cancelButton = useRef<HTMLButtonElement | null>(null);
  /** Set by whichever path closed the session, read by the effect below. */
  const returnFocus = useRef(false);
  /**
   * Set when the user asked to start editing.
   *
   * "Edit layout" is replaced by Cancel and Save rather than relabelled, so
   * the button the user just pressed leaves the document and the browser
   * drops focus to the body. Moving it to Cancel keeps the keyboard where the
   * controls now are, and makes the first Tab land inside the editor instead
   * of at the top of the page.
   */
  const focusToolbar = useRef(false);

  const editing = session.value !== null;

  // The stored layout decides which sources are fetched, so this re-runs when
  // the load replaces the default with the user's arrangement.
  //
  // It runs first against the default, before that load has landed, and that
  // is the trade wanted: first paint does not wait on the layout round trip.
  // For the unsaved-dashboard case the two source sets are identical anyway.
  // For a customized one it can fetch a source only a default widget reads --
  // `ensure` is keyed per source and range, so the cost is those few requests
  // once, not a refetch of everything.
  useEffect(() => {
    if (!IS_BROWSER) return;
    dashboardData.ensure(sourcesFor(layout.value), timeRange.value);
  }, [timeRange.value, layout.value]);

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

  // After the render that ended edit mode, not during the key press that asked
  // for it. Focusing first leaves the browser about to run its own focus
  // fix-up on the widget it is removing from the tab order, and that lands on
  // the document rather than on the button we just moved to.
  useLayoutEffect(() => {
    if (!IS_BROWSER) return;
    if (editing) {
      if (!focusToolbar.current) return;
      focusToolbar.current = false;
      cancelButton.current?.focus();
      return;
    }
    if (!returnFocus.current) return;
    returnFocus.current = false;
    editButton.current?.focus();
  }, [editing]);

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
    focusToolbar.current = true;
    session.value = beginEdit(asRendered(layout.value), layoutRevision.value);
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
    session.value = null;
    returnFocus.current = true;
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
      returnFocus.current = true;
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
    returnFocus.current = true;
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

        <div style={{ display: "flex", alignItems: "center", gap: "8px" }}>
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
            editButtonRef={editButton}
            cancelButtonRef={cancelButton}
            onEdit={startEditing}
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
        onExitEdit={requestExit}
      />

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
