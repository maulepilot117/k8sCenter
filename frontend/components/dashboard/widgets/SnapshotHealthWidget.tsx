import StatusBadge from "@/components/ui/glass/StatusBadge.tsx";
import { SeverityCount } from "@/components/ui/ScanBadges.tsx";
import WidgetShell from "@/components/ui/WidgetShell.tsx";
import { dashboardData } from "@/lib/dashboard/data.ts";
// The outcome ranking, the error-over-ready precedence and the unreadable-row
// accounting live in lib/, under test, because none of them is a field read
// (D-10, KTD8). Do not inline them.
import type { SnapshotRow } from "@/lib/dashboard/expiry.ts";
import {
  SNAPSHOTS_PAGE_HREF,
  snapshotHealthView,
} from "@/lib/dashboard/expiry.ts";
import { registerWidget } from "@/lib/dashboard/registry.ts";

/** How many snapshots the card lists. The counts above them cover every
 * snapshot the endpoint returned, not just these. */
const ROW_LIMIT = 5;

const WORST_COLORS: Record<string, string> = {
  error: "var(--error)",
  pending: "var(--warning)",
  ready: "var(--success)",
};

/**
 * Whether the cluster's VolumeSnapshots are actually usable.
 *
 * A roll-up by outcome, ranked: an errored snapshot outranks one still being
 * cut, which outranks a ready one. `readyToUse` and `status.error.message` are
 * independent fields on the CRD and a snapshot can carry both, so the error
 * wins -- a snapshot advertising itself as ready while carrying an error
 * message is the one worth looking at, and rounding it to "ready" is how it
 * stops being looked at.
 *
 * Absence is detected differently here than on the other three
 * data-protection cards, because the storage family is the one CRD-discovered
 * feature with no `/status` route. VolumeSnapshot is discovered exactly like
 * cert-manager, ESO and Velero -- `checkSnapshotCRDs` asks the discovery
 * client for `snapshot.storage.k8s.io/v1` behind a 5-minute cache -- but the
 * answer is published as the `available` flag on the snapshot routes'
 * metadata rather than as a status body. The `snapshots-status` source reads
 * that flag off the cheap snapshot-CLASSES route and normalises it into the
 * shape the shell already understands, so this card gets the same explicit
 * "not installed on this cluster" state as its neighbours without a new
 * backend handler (R1, KTD1). See `snapshots-status` in lib/dashboard/types.ts.
 *
 * That matters more here than almost anywhere else in the catalog: a cluster
 * with no CSI snapshotter and a cluster whose snapshots all succeeded both
 * answer 200 with an empty array, and one of those two readings is a cluster
 * with no volume-level recovery at all.
 */
function SnapshotHealth() {
  const view = snapshotHealthView(
    dashboardData.state("snapshots-list").data,
    ROW_LIMIT,
  );
  const attention = view.counts.error + view.counts.pending;

  return (
    <WidgetShell
      title="Volume Snapshots"
      action={
        view.readable && view.worst !== null ? (
          <span
            data-testid="snapshot-health-summary"
            class="text-xs"
            style={{ color: WORST_COLORS[view.worst] ?? "var(--text-muted)" }}
          >
            {view.total} snapshot{view.total === 1 ? "" : "s"}
          </span>
        ) : undefined
      }
    >
      {!view.readable ? (
        // The snapshots route answered with something this build cannot read.
        // An empty list here would read as a cluster with nothing broken.
        <p
          data-testid="snapshot-health-unreadable"
          class="py-4 text-center text-xs text-text-muted"
        >
          The snapshots endpoint returned a result this card cannot read.
        </p>
      ) : view.total === 0 ? (
        // The snapshot CRDs are installed -- the family status said so -- and
        // nothing has been snapshotted. Not an absence, and not good news.
        <p
          data-testid="snapshot-health-none"
          class="py-4 text-center text-xs text-text-muted"
        >
          Volume snapshots are supported here but none you can see exists.
        </p>
      ) : (
        <>
          <div
            class="mb-3 flex flex-wrap gap-1.5"
            data-testid="snapshot-health-counts"
          >
            <SeverityCount
              label="errored"
              count={view.counts.error}
              color="var(--error)"
            />
            <SeverityCount
              label="pending"
              count={view.counts.pending}
              color="var(--warning)"
            />
            <SeverityCount
              label="ready"
              count={view.counts.ready}
              color="var(--success)"
            />
          </div>

          {attention === 0 ? (
            <p
              data-testid="snapshot-health-clear"
              class="py-2 text-center text-xs text-text-muted"
            >
              All {view.total} snapshot{view.total === 1 ? " is" : "s are"}{" "}
              ready to restore from.
            </p>
          ) : (
            <ul class="flex flex-col gap-2" data-testid="snapshot-health-list">
              {view.rows.map((row) => (
                <SnapshotItem key={`${row.namespace}/${row.name}`} row={row} />
              ))}
            </ul>
          )}

          {attention > view.rows.length && (
            <p
              data-testid="snapshot-health-more"
              class="mt-2 text-[11px] leading-snug text-text-muted"
            >
              Showing the {view.rows.length} worst of {attention} that are not
              ready.
            </p>
          )}
        </>
      )}

      {view.unreadableRows > 0 && (
        <p
          data-testid="snapshot-health-unreadable-rows"
          class="mt-2 text-[11px] leading-snug text-text-muted"
        >
          {view.unreadableRows} entr
          {view.unreadableRows === 1 ? "y" : "ies"} named no snapshot this card
          could render and {view.unreadableRows === 1 ? "was" : "were"} left
          out.
        </p>
      )}

      <a
        href={SNAPSHOTS_PAGE_HREF}
        class="mt-3 block text-xs text-accent no-underline"
      >
        View all snapshots →
      </a>
    </WidgetShell>
  );
}

/**
 * One snapshot's row. Not a link: there is no per-snapshot route -- the
 * snapshots page lists them in a table -- so the card links to the list rather
 * than inventing a URL that would 404.
 *
 * The second line carries the controller's error message when there is one,
 * because "not ready" without a reason sends the reader to the same page they
 * are already looking at a summary of.
 */
function SnapshotItem({ row }: { row: SnapshotRow }) {
  return (
    <li class="flex flex-col gap-1">
      <span class="flex items-center justify-between gap-2">
        <span
          class="min-w-0 truncate text-xs text-text-secondary"
          title={`${row.namespace}/${row.name}`}
        >
          {row.name}
        </span>
        <span class="shrink-0">
          <StatusBadge
            label={row.outcome === "error" ? "Error" : "Pending"}
            tone={row.outcome === "error" ? "crit" : "warn"}
          />
        </span>
      </span>
      <span
        class="truncate text-[10px] text-text-muted"
        title={row.errorMessage}
      >
        {row.namespace}
        {row.sourcePVC === "" ? "" : ` · ${row.sourcePVC}`}
        {row.errorMessage === "" ? "" : ` · ${row.errorMessage}`}
      </span>
    </li>
  );
}

registerWidget({
  id: "snapshot-health",
  title: "Volume Snapshots",
  family: "data-protection",
  scopes: ["overview"],
  sources: ["snapshots-list"],
  // The eighth family status key, added by this unit. VolumeSnapshot is
  // CRD-discovered like the other three families here, but the storage routes
  // publish the discovery answer as `metadata.available` rather than through a
  // `/status` route -- so `snapshots-status` reads that flag off the cheap
  // snapshot-classes route and normalises it. Without it the card would report
  // a cluster with no CSI snapshotter as one whose snapshots are all fine
  // (R1, KTD1).
  familyStatus: "snapshots-status",
  // Pinned on five sides -- here, two literals in registry_test.ts, the Go
  // catalog and two literals in parity_test.go (KTD7). Chosen once. Wider than
  // the three-column minimum: a snapshot name carries its source claim and a
  // date, beside a state badge.
  minW: 4,
  minH: 3,
  defaultW: 4,
  defaultH: 5,
  modes: ["normal"],
  render: () => <SnapshotHealth />,
});
