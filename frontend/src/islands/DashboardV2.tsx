import { useSignal } from "@preact/signals";
import type { JSX } from "preact";
import { useEffect, useLayoutEffect, useRef } from "preact/hooks";
import DashboardGrid from "@/components/dashboard/DashboardGrid.tsx";
import { Alert } from "@/components/ui/Alert.tsx";
import { Skeleton } from "@/components/ui/Skeleton.tsx";
// Registers every shipped widget before first render.
import "@/components/dashboard/widgets/index.ts";
import { dashboardData } from "@/lib/dashboard/data.ts";
import type { LayoutUnavailable } from "@/lib/dashboard/layout-store.ts";
import {
  layout,
  layoutGeneration,
  layoutUnavailable,
  layoutWarnings,
  layoutWithheld,
  loadLayout,
} from "@/lib/dashboard/layout-store.ts";
import { getWidget } from "@/lib/dashboard/registry.ts";
import type {
  DashboardLayoutConfig,
  DataSourceKey,
} from "@/lib/dashboard/types.ts";
import type {
  ClusterInfoData,
  DashboardSummary,
} from "@/lib/dashboard/wire-types.ts";
import { IS_BROWSER } from "@/src/lib/is-browser.ts";

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
  switch (reason) {
    case "database_unavailable":
      return "Saved dashboard layouts need a database, and this deployment has none configured.";
    case "identity_too_long":
      return "Your account identity is longer than stored layouts support. An operator has to shorten the mapped identity attribute.";
    case "invalid_config":
    case "unsupported_schema_version":
    case "unknown_widget_id":
      return "Your stored layout no longer describes something this server can read.";
    default:
      return "Your saved layout could not be loaded.";
  }
}

export default function DashboardV2() {
  const timeRange = useSignal<TimeRange>("1h");
  // Edit mode is deliberately not persisted. The layout it produces now is:
  // P3 stores it, and P4 adds the Save button that writes the working copy
  // back through the layout store.
  const editing = useSignal(false);
  // Escape on a focused widget leaves edit mode, which takes that widget out
  // of the tab order under the focus that is on it. Focus has to land
  // somewhere deliberate, and where editing started is the only place the user
  // asked for.
  const editButton = useRef<HTMLButtonElement | null>(null);
  /** Set by Escape, read by the effect below. Only that exit needs to move
   * focus: clicking "Done" leaves it on the button already. */
  const returnFocus = useRef(false);

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

  // After the render that ended edit mode, not during the key press that asked
  // for it. Focusing first leaves the browser about to run its own focus
  // fix-up on the widget it is removing from the tab order, and that lands on
  // the document rather than on the button we just moved to.
  useLayoutEffect(() => {
    if (!IS_BROWSER || editing.value || !returnFocus.current) return;
    returnFocus.current = false;
    editButton.current?.focus();
  }, [editing.value]);

  useEffect(() => {
    if (!IS_BROWSER) return;
    const stop = dashboardData.startRefresh();
    return () => {
      stop();
      dashboardData.abort();
    };
  }, []);

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
  // Changes only when a load replaces the layout, which is when the grid has
  // to start over from a new arrangement. See the comment at its usage.
  const layoutKey = `layout-${layoutGeneration.value}`;

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
          <button
            ref={editButton}
            type="button"
            data-testid="edit-layout"
            aria-pressed={editing.value}
            // Arranging a dashboard that cannot be stored is work the user
            // loses on the next reload, so the affordance is withdrawn rather
            // than offered and then disappointed.
            //
            // Withheld placements do NOT disable it, although they also block
            // a save: there the layout on screen is the user's own and the
            // block is transient, so editing it for this session is still
            // worth something. A failed load is different -- what is on screen
            // is the shipped default, and nothing about arranging it survives.
            disabled={storageDown}
            title={storageDown ? (unavailableMessage ?? undefined) : undefined}
            onClick={() => {
              editing.value = !editing.value;
            }}
            style={{
              padding: "7px 14px",
              borderRadius: "8px",
              border: "1px solid var(--glass-border)",
              cursor: storageDown ? "not-allowed" : "pointer",
              opacity: storageDown ? 0.5 : 1,
              fontSize: "12px",
              fontWeight: 500,
              background: editing.value
                ? "var(--accent)"
                : "var(--glass-surface)",
              color: editing.value ? "var(--bg-base)" : "var(--text-muted)",
              transition: "background 0.15s, color 0.15s",
            }}
          >
            {editing.value ? "Done" : "Edit layout"}
          </button>

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

      {/*
        Keyed on the loaded layout. DashboardGrid copies `initial` into its own
        signal once, on mount, because the working copy is what the pointer
        sessions reshape -- so a prop change alone would leave the grid still
        rendering the default after the stored layout lands. Re-mounting is the
        honest way to hand it a new starting point, and it is the same thing a
        reset or a scope change will need in P4.
      */}
      <DashboardGrid
        key={layoutKey}
        initial={layout.value}
        editable={editing.value}
        onExitEdit={() => {
          returnFocus.current = true;
          editing.value = false;
        }}
      />
    </div>
  );
}
