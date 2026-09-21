import type { Tone } from "@/components/ui/glass/StatusBadge.tsx";
import StatusBadge from "@/components/ui/glass/StatusBadge.tsx";
import { SeverityCount } from "@/components/ui/ScanBadges.tsx";
import WidgetShell from "@/components/ui/WidgetShell.tsx";
import { dashboardData } from "@/lib/dashboard/data.ts";
// The state vocabulary, the worst-first roll-up and the unreadable-row
// accounting live in lib/, under test, because none of them is a field read
// (D-10, KTD8). Do not inline them.
import type { ClusterRow, ClusterState } from "@/lib/dashboard/platform.ts";
import {
  CLUSTERS_PAGE_HREF,
  clusterStatusView,
} from "@/lib/dashboard/platform.ts";
import { registerWidget } from "@/lib/dashboard/registry.ts";

/** How many clusters the card lists. The counts above them cover every cluster
 * the registry returned, not just these. */
const ROW_LIMIT = 5;

/** What each state reads as, and in which colour. `unknown` is muted rather
 * than warned: a status this build does not recognise is a gap in what we can
 * say, not a fault we have observed. */
const STATE_TONE: Record<ClusterState, Tone> = {
  error: "crit",
  blocked: "crit",
  disconnected: "warn",
  unknown: "neutral",
  connected: "ok",
};

/**
 * Whether the clusters this install manages are actually reachable.
 *
 * The registry's own record, which is what ClusterProber writes every 60s and
 * what the clusters page renders -- not a live probe. A card that re-probed
 * would disagree with that page for up to a minute at a time and would put
 * five API-server dials behind an overview screen.
 *
 * Two absences this card must not conflate, and it conflates neither:
 *
 * - A deployment with no database answers 503 here, not an empty list. That is
 *   a deployment-configuration gap rather than a cluster fact, and it is the
 *   one case in this catalog that arrives as an error status instead of a
 *   discovery payload -- `ABSENT_STATUSES` in lib/dashboard/types.ts maps it
 *   onto the same unavailable state a missing operator gets, so the card never
 *   renders "no clusters" at an operator who has simply not wired PostgreSQL.
 * - A non-admin is refused with 403, because `/v1/clusters` is gated by
 *   `middleware.RequireAdmin`. That resolves to the permission state, with no
 *   retry, and the palette marks the entry before it is ever added.
 *
 * What is left for this function is the honest middle: a registry that
 * answered, holding at least the local cluster.
 */
function ClusterStatus() {
  const view = clusterStatusView(
    dashboardData.state("clusters-list").data,
    ROW_LIMIT,
  );
  const unhealthy =
    view.counts.error +
    view.counts.blocked +
    view.counts.disconnected +
    view.counts.unknown;

  return (
    <WidgetShell
      title="Clusters"
      action={
        view.readable && view.total > 0 ? (
          <span
            data-testid="cluster-status-summary"
            class="text-xs text-text-muted"
          >
            {view.total} cluster{view.total === 1 ? "" : "s"}
          </span>
        ) : undefined
      }
    >
      {!view.readable ? (
        // The registry answered with something this build cannot read. An
        // empty list here would read as an install managing nothing.
        <p
          data-testid="cluster-status-unreadable"
          class="py-4 text-center text-xs text-text-muted"
        >
          The clusters endpoint returned a result this card cannot read.
        </p>
      ) : view.total === 0 ? (
        // Reachable only if the registry has been emptied: a deployment with a
        // database seeds its own local cluster at boot (`EnsureLocal`). Said
        // plainly rather than as good news, because an install managing no
        // clusters is not a healthy install.
        <p
          data-testid="cluster-status-none"
          class="py-4 text-center text-xs text-text-muted"
        >
          No clusters are registered, not even this one.
        </p>
      ) : (
        <>
          <div
            class="mb-3 flex flex-wrap gap-1.5"
            data-testid="cluster-status-counts"
          >
            <SeverityCount
              label="error"
              count={view.counts.error}
              color="var(--error)"
            />
            <SeverityCount
              label="blocked"
              count={view.counts.blocked}
              color="var(--error)"
            />
            <SeverityCount
              label="disconnected"
              count={view.counts.disconnected}
              color="var(--warning)"
            />
            <SeverityCount
              label="unknown"
              count={view.counts.unknown}
              color="var(--text-muted)"
            />
            <SeverityCount
              label="connected"
              count={view.counts.connected}
              color="var(--success)"
            />
          </div>

          <ul class="flex flex-col gap-2" data-testid="cluster-status-list">
            {view.rows.map((row) => (
              <ClusterItem key={row.id} row={row} />
            ))}
          </ul>

          {unhealthy === 0 && view.rows.length === view.total && (
            <p
              data-testid="cluster-status-clear"
              class="mt-2 text-center text-[11px] leading-snug text-text-muted"
            >
              Every registered cluster last probed as connected.
            </p>
          )}

          {view.total > view.rows.length && (
            <p
              data-testid="cluster-status-more"
              class="mt-2 text-[11px] leading-snug text-text-muted"
            >
              Showing the {view.rows.length} worst of {view.total}.
            </p>
          )}
        </>
      )}

      {view.unreadableRows > 0 && (
        <p
          data-testid="cluster-status-unreadable-rows"
          class="mt-2 text-[11px] leading-snug text-text-muted"
        >
          {view.unreadableRows} entr
          {view.unreadableRows === 1 ? "y" : "ies"} named no cluster this card
          could render and {view.unreadableRows === 1 ? "was" : "were"} left
          out.
        </p>
      )}

      <a
        href={CLUSTERS_PAGE_HREF}
        class="mt-3 block text-xs text-accent no-underline"
      >
        Manage clusters →
      </a>
    </WidgetShell>
  );
}

/**
 * One cluster's row. Not a link: the clusters page opens its detail in a
 * drawer over the table rather than at a per-cluster route, so the card links
 * to the page once and leaves rows as text instead of inventing a URL.
 */
function ClusterItem({ row }: { row: ClusterRow }) {
  return (
    <li class="flex flex-col gap-1">
      <span class="flex items-center justify-between gap-2">
        <span
          class="min-w-0 truncate text-xs text-text-secondary"
          title={row.id}
        >
          {row.name}
          {row.isLocal && (
            <span class="ml-1.5 text-[10px] text-text-muted">local</span>
          )}
        </span>
        <span class="shrink-0">
          <StatusBadge label={row.state} tone={STATE_TONE[row.state]} />
        </span>
      </span>
      <span class="truncate text-[10px] text-text-muted">
        {row.statusMessage !== ""
          ? row.statusMessage
          : [
              row.version === "" ? null : row.version,
              // Null, not zero: a count the record did not carry is not a
              // cluster with no nodes.
              row.nodeCount === null
                ? null
                : `${row.nodeCount} node${row.nodeCount === 1 ? "" : "s"}`,
            ]
              .filter((s) => s !== null)
              .join(" · ")}
      </span>
    </li>
  );
}

registerWidget({
  id: "cluster-status",
  title: "Clusters",
  family: "platform",
  scopes: ["overview"],
  sources: ["clusters-list"],
  // No `familyStatus`: multi-cluster management is not CRD-discovered, so
  // there is no discovery route to declare. What can be missing is the
  // deployment's database, which `/v1/clusters` reports as a 503 rather than
  // as an absent feature -- see `ABSENT_STATUSES` in lib/dashboard/types.ts.
  //
  // `/v1/clusters` is gated by `middleware.RequireAdmin`, so the palette can
  // mark this entry for a non-admin before it is added. That is a session
  // fact rather than a cluster one, which is why it is declared here instead
  // of fetched.
  adminOnly: true,
  // Pinned on five sides -- here, two literals in registry_test.ts, the Go
  // catalog and two literals in parity_test.go (KTD7). Chosen once. Four
  // columns for the reason the data-protection cards are: a row carries a
  // cluster name beside a state badge, above a probe message that is
  // routinely a dial error with an address in it.
  minW: 4,
  minH: 3,
  defaultW: 4,
  defaultH: 5,
  modes: ["normal"],
  render: () => <ClusterStatus />,
});
