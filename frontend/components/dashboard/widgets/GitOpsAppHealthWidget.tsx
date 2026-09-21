import {
  HealthStatusBadge,
  SyncStatusBadge,
  ToolBadge,
} from "@/components/ui/GitOpsBadges.tsx";
import { SeverityCount } from "@/components/ui/ScanBadges.tsx";
import WidgetShell from "@/components/ui/WidgetShell.tsx";
import { dashboardData } from "@/lib/dashboard/data.ts";
import { registerWidget } from "@/lib/dashboard/registry.ts";
// The fold from two independent status fields to one bucket per application,
// the severity ordering behind it and the per-tool tallies live in lib/, under
// test, because none of them is a field read (D-10, KTD8). Do not inline them.
import type { AppHealthRow } from "@/lib/dashboard/sync-state.ts";
import {
  GITOPS_APPLICATIONS_PAGE_HREF,
  gitopsAppHealthView,
} from "@/lib/dashboard/sync-state.ts";
import type { HealthStatus, SyncStatus } from "@/lib/gitops-types.ts";

/** How many applications the card lists. The counts above them cover every one
 * the endpoint returned, not just these. */
const ROW_LIMIT = 5;

/**
 * Whether what git says is running is what is actually running.
 *
 * One read, `/v1/gitops/applications`, spanning both tools: Argo CD
 * Applications and Flux Kustomizations and HelmReleases arrive normalised into
 * one list, each keyed by the feature's `{tool}:{namespace}:{name}` composite
 * id and each carrying the tool that manages it. The roll-up is therefore over
 * the whole fleet and every row stays attributable to one tool, rather than
 * the card splitting into an Argo half and a Flux half that an operator has to
 * add up.
 *
 * The counts are NOT the response's own `summary`. That object counts sync and
 * health in two independent switches, so an application that is out of sync
 * AND degraded lands in both of its counters -- correct for the applications
 * page, which lists the same application under either filter, and wrong here,
 * where the numbers sit side by side and invite addition. `gitopsAppHealthView`
 * assigns one bucket per application, worst wins, and the buckets sum to the
 * total. See sync-state.ts.
 *
 * Absence and a quiet fleet are opposite readings of the same response. GitOps
 * is CRD-discovered and this route answers 200 with an empty result whether
 * neither tool is installed or both are installed with nothing to report; the
 * declared `gitops-status` family is what tells them apart, resolved by the
 * shell before `render` is called (R1, KTD1).
 */
function GitOpsAppHealth() {
  const view = gitopsAppHealthView(
    dashboardData.state("gitops-applications").data,
    ROW_LIMIT,
  );

  return (
    <WidgetShell
      title="GitOps Applications"
      action={
        view.readable && view.total > 0 ? (
          <span
            data-testid="gitops-app-health-summary"
            class="text-xs"
            style={{
              color:
                view.counts.degraded > 0
                  ? "var(--error)"
                  : view.attention > 0
                    ? "var(--warning)"
                    : "var(--success)",
            }}
          >
            {view.counts.synced} of {view.total} synced
          </span>
        ) : undefined
      }
    >
      {!view.readable ? (
        // The applications route answered with something this build cannot
        // read. An empty list here would read as a fleet with nothing wrong.
        <p
          data-testid="gitops-app-health-unreadable"
          class="py-4 text-center text-xs text-text-muted"
        >
          The GitOps endpoint returned a result this card cannot read.
        </p>
      ) : view.total === 0 ? (
        // A tool is installed -- the family status said so -- and manages
        // nothing you can see. Distinct from both "no GitOps" and "all synced".
        <p
          data-testid="gitops-app-health-none"
          class="py-4 text-center text-xs text-text-muted"
        >
          GitOps is installed but no application you can see is managed yet.
        </p>
      ) : (
        <>
          <div
            class="mb-2 flex flex-wrap gap-1.5"
            data-testid="gitops-app-health-counts"
          >
            <SeverityCount
              label="degraded"
              count={view.counts.degraded}
              color="var(--error)"
            />
            <SeverityCount
              label="out of sync"
              count={view.counts.outofsync}
              color="var(--warning)"
            />
            <SeverityCount
              label="progressing"
              count={view.counts.progressing}
              color="var(--accent)"
            />
            <SeverityCount
              label="suspended"
              count={view.counts.suspended}
              color="var(--text-muted)"
            />
            <SeverityCount
              label="unknown"
              count={view.counts.unknown}
              color="var(--accent-secondary)"
            />
            <SeverityCount
              label="synced"
              count={view.counts.synced}
              color="var(--success)"
            />
          </div>

          {/* Which tools the roll-up above spans. One tool is the common case
              and the line still earns its place: a cluster running both is
              exactly the one where "3 out of sync" is not actionable without
              knowing which tool to open. */}
          <div
            class="mb-3 flex flex-wrap items-center gap-2"
            data-testid="gitops-app-health-tools"
          >
            {view.tools.map((t) => (
              <span key={t.tool} class="flex items-center gap-1">
                <ToolBadge tool={t.tool} />
                <span class="text-[11px] text-text-muted">{t.total}</span>
              </span>
            ))}
          </div>

          {view.attention === 0 ? (
            <p
              data-testid="gitops-app-health-clear"
              class="py-2 text-center text-xs text-text-muted"
            >
              Every application you can see is synced and healthy.
            </p>
          ) : (
            <ul
              class="flex flex-col gap-2"
              data-testid="gitops-app-health-list"
            >
              {view.rows.map((row) => (
                <ApplicationItem key={row.id} row={row} />
              ))}
            </ul>
          )}

          {view.attention > view.rows.length && (
            <p
              data-testid="gitops-app-health-more"
              class="mt-2 text-[11px] leading-snug text-text-muted"
            >
              Showing the {view.rows.length} worst of {view.attention} needing
              attention.
            </p>
          )}
        </>
      )}

      {view.unreadableRows > 0 && (
        <p
          data-testid="gitops-app-health-unreadable-rows"
          class="mt-2 text-[11px] leading-snug text-text-muted"
        >
          {view.unreadableRows} entr
          {view.unreadableRows === 1 ? "y" : "ies"} named no application this
          card could render and {view.unreadableRows === 1 ? "was" : "were"}{" "}
          left out.
        </p>
      )}

      <a
        href={GITOPS_APPLICATIONS_PAGE_HREF}
        class="mt-3 block text-xs text-accent no-underline"
      >
        View all applications →
      </a>
    </WidgetShell>
  );
}

/**
 * One application's row.
 *
 * Both badges are the GitOps surface's own rather than chips styled here: the
 * same word in two colours on two pages is a bug in the colour. The health
 * badge appears only when it is the reason the row is here -- a degraded
 * application -- because an out-of-sync application with healthy workloads
 * gains nothing from a green "Healthy" beside the finding.
 */
function ApplicationItem({ row }: { row: AppHealthRow }) {
  return (
    <li class="flex flex-col gap-1">
      <span class="flex items-center justify-between gap-2">
        <a
          href={row.href}
          title={`${row.namespace}/${row.name}`}
          class="min-w-0 truncate text-xs text-text-secondary no-underline hover:text-accent"
        >
          {row.name}
        </a>
        <span class="flex shrink-0 items-center gap-1">
          {row.bucket === "degraded" && (
            <HealthStatusBadge status={row.healthStatus as HealthStatus} />
          )}
          <SyncStatusBadge status={row.syncStatus as SyncStatus} />
        </span>
      </span>
      <span class="flex items-center gap-1.5 truncate text-[10px] text-text-muted">
        <ToolBadge tool={row.tool} />
        {row.namespace}
        {row.message === "" ? "" : ` · ${row.message}`}
      </span>
    </li>
  );
}

registerWidget({
  id: "gitops-app-health",
  title: "GitOps Applications",
  family: "delivery",
  scopes: ["overview"],
  // One read, shared with `gitops-recent-syncs`: the cache issues it once per
  // cycle however many of the two cards are on the layout.
  sources: ["gitops-applications"],
  // GitOps is CRD-discovered, so a cluster with neither Argo CD nor Flux and a
  // cluster with both and nothing to report send the same 200 with the same
  // empty result. Without this the card would report a cluster with no
  // continuous delivery at all as one where everything is in sync (R1, KTD1).
  familyStatus: "gitops-status",
  // Pinned on five sides -- here, two literals in registry_test.ts, the Go
  // catalog and two literals in parity_test.go (KTD7). Chosen once. Four
  // columns because a row carries an application name beside two badges and an
  // attribution line, and an Argo CD application name is routinely long.
  minW: 4,
  minH: 3,
  defaultW: 4,
  defaultH: 5,
  modes: ["normal"],
  render: () => <GitOpsAppHealth />,
});
