import WidgetShell from "@/components/ui/WidgetShell.tsx";
import { selectedCluster } from "@/lib/cluster.ts";
import { dashboardData } from "@/lib/dashboard/data.ts";
// The per-cluster filter, the other-cluster accounting and the href
// resolution live in lib/, under test (D-10, KTD8).
import type { PinnedRow } from "@/lib/dashboard/platform.ts";
import {
  PREFERENCES_START_HREF,
  pinnedResourcesView,
} from "@/lib/dashboard/platform.ts";
import { registerWidget } from "@/lib/dashboard/registry.ts";

/** How many pins the card lists. The count beside them covers every pin on
 * this cluster. */
const ROW_LIMIT = 6;

/**
 * The objects this account has pinned on the cluster being viewed.
 *
 * The sidebar already carries these, and this card is not a second copy of it:
 * the sidebar's list is scoped to the navigation and scrolls away, where a
 * dashboard placement keeps the handful of objects an operator actually
 * watches in the same field of view as the cluster's health.
 *
 * Like the sidebar, it shows STORED IDENTITY ONLY. It does not fetch the
 * objects to see whether they still exist, whether this account can still read
 * them, or whether the uid still matches -- that classification happens on the
 * detail page, where the object is being fetched anyway. Resolving them here
 * would mean an N+1 fan-out on every dashboard refresh.
 *
 * Pins on other clusters are withheld rather than shown: a pin names one
 * object in one cluster and cannot be opened from here
 * (`pinsForActiveCluster`). They are COUNTED, because withholding them
 * silently would say "you have no pinned resources" to someone holding twenty.
 */
function PinnedResources() {
  const view = pinnedResourcesView(
    dashboardData.state("preference-pins").data,
    selectedCluster.value,
    ROW_LIMIT,
  );

  return (
    <WidgetShell
      title="Pinned"
      action={
        view.readable && view.total > 0 ? (
          <span
            data-testid="pinned-resources-summary"
            class="text-xs text-text-muted"
          >
            {view.total} pinned
          </span>
        ) : undefined
      }
    >
      {!view.readable ? (
        <p
          data-testid="pinned-resources-unreadable"
          class="py-4 text-center text-xs text-text-muted"
        >
          The preferences endpoint returned a result this card cannot read.
        </p>
      ) : view.total === 0 ? (
        // The empty state has to say where a pin comes from: there is no
        // "pins" page, because a pin is made from the control on a resource's
        // own detail page.
        <div
          data-testid="pinned-resources-none"
          class="flex flex-col items-center gap-1.5 py-4 text-center"
        >
          <p class="text-xs text-text-muted">
            Nothing is pinned on this cluster.
          </p>
          <a
            href={PREFERENCES_START_HREF}
            class="text-xs text-accent no-underline"
          >
            Open a resource and pin it →
          </a>
        </div>
      ) : (
        <>
          <ul class="flex flex-col gap-1.5" data-testid="pinned-resources-list">
            {view.rows.map((row) => (
              <PinnedItem key={row.id} row={row} />
            ))}
          </ul>

          {view.total > view.rows.length && (
            <p
              data-testid="pinned-resources-more"
              class="mt-2 text-[11px] leading-snug text-text-muted"
            >
              Showing {view.rows.length} of {view.total}.
            </p>
          )}
        </>
      )}

      {view.otherClusters > 0 && (
        <p
          data-testid="pinned-resources-other-clusters"
          class="mt-2 text-[11px] leading-snug text-text-muted"
        >
          {view.otherClusters} more{" "}
          {view.otherClusters === 1 ? "pin is" : "pins are"} held on other
          clusters and cannot be opened from here.
        </p>
      )}

      {view.unreadableRows > 0 && (
        <p
          data-testid="pinned-resources-unreadable-rows"
          class="mt-2 text-[11px] leading-snug text-text-muted"
        >
          {view.unreadableRows} record
          {view.unreadableRows === 1 ? "" : "s"} named no object this card could
          render and {view.unreadableRows === 1 ? "was" : "were"} left out.
        </p>
      )}
    </WidgetShell>
  );
}

/**
 * One pinned object, linking to its detail page.
 *
 * A pin whose kind this build routes no page for renders as text rather than
 * as a link that would 404 -- the same rule `resourceHref` already applies in
 * the sidebar, and the same reason the row keeps its kind either way.
 */
function PinnedItem({ row }: { row: PinnedRow }) {
  const label = (
    <>
      <span
        class="min-w-0 truncate text-xs text-text-secondary"
        title={row.name}
      >
        {row.name}
      </span>
      <span class="shrink-0 font-mono text-[10px] text-text-muted">
        {row.kind === "" ? "unknown kind" : row.kind}
        {row.namespace === "" ? "" : ` · ${row.namespace}`}
      </span>
    </>
  );

  return (
    <li class="flex items-center justify-between gap-2">
      {row.href === null ? (
        label
      ) : (
        <a
          href={row.href}
          class="flex min-w-0 flex-1 items-center justify-between gap-2 no-underline"
        >
          {label}
        </a>
      )}
    </li>
  );
}

registerWidget({
  id: "pinned-resources",
  title: "Pinned",
  family: "platform",
  scopes: ["overview"],
  sources: ["preference-pins"],
  // No `familyStatus` and no `adminOnly`, for the reasons `saved-views`
  // carries: pins are per-user, open to every authenticated account, and their
  // absence is the deployment's missing database.
  //
  // No `params` either, although the card is cluster-scoped. The cluster comes
  // from `selectedCluster` -- the one the whole dashboard is pointed at -- not
  // from a stored value, so there is nothing for the server to validate and
  // nothing to go stale when the user switches clusters.
  //
  // Pinned on five sides (KTD7). Three columns: a row is an object name beside
  // its kind, both short.
  minW: 3,
  minH: 3,
  defaultW: 3,
  defaultH: 5,
  modes: ["normal"],
  render: () => <PinnedResources />,
});
