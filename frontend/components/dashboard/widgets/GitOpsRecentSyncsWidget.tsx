import { SyncStatusBadge, ToolBadge } from "@/components/ui/GitOpsBadges.tsx";
import WidgetShell from "@/components/ui/WidgetShell.tsx";
import { dashboardData } from "@/lib/dashboard/data.ts";
import { registerWidget } from "@/lib/dashboard/registry.ts";
// The ranking, the never-synced accounting and the revision shortening live in
// lib/, under test (D-10, KTD8). The revision shortening in particular is not
// a substring: Flux writes `{branch}@sha1:{sha}`, and a blind truncation to
// seven characters prints `main@sh`.
import type { RecentSyncRow } from "@/lib/dashboard/sync-state.ts";
import {
  GITOPS_APPLICATIONS_PAGE_HREF,
  gitopsRecentSyncsView,
} from "@/lib/dashboard/sync-state.ts";
import type { SyncStatus } from "@/lib/gitops-types.ts";
import { timeAgo } from "@/lib/timeAgo.ts";

/** How many syncs the card lists. */
const ROW_LIMIT = 6;

/**
 * What has actually been deployed lately, and by which tool.
 *
 * The same one read as `gitops-app-health` -- the cache issues
 * `/v1/gitops/applications` once per cycle however many of the two cards are
 * placed -- ranked by `lastSyncTime` rather than rolled up. Both tools appear
 * in one timeline, which is the view neither tool's own UI can give on a
 * cluster running both.
 *
 * COMMIT ENRICHMENT IS NOT THIS CARD'S DATA SOURCE, and that is a decision
 * rather than an omission. `/v1/gitops/commits` needs a repository URL and a
 * set of shas, so it can only be asked once this list is already in hand, and
 * it answers with a neutral empty shape when the deployment has no Git
 * provider token configured -- which is the default. A card whose rows carried
 * commit titles would therefore be blank on most clusters. Every field below
 * comes off the applications payload: which application, which tool, when, to
 * which revision, with what result. A commit title, if enrichment is ever
 * added, layers onto a row that already reads correctly without one.
 *
 * An application that has never synced is counted, not ranked. Placing it at
 * the top of a "recent syncs" list, or at the bottom dated the epoch, would
 * both be inventions.
 *
 * Absence and a quiet fleet are opposite readings of the same response, which
 * is why `gitops-status` is declared and resolved by the shell before `render`
 * is called (R1, KTD1).
 */
function GitOpsRecentSyncs() {
  const view = gitopsRecentSyncsView(
    dashboardData.state("gitops-applications").data,
    ROW_LIMIT,
  );

  return (
    <WidgetShell
      title="Recent Syncs"
      action={
        view.readable && view.rows.length > 0 ? (
          <span
            data-testid="gitops-recent-syncs-summary"
            class="text-xs text-text-muted"
          >
            {view.rows.length} of {view.total - view.untimed}
          </span>
        ) : undefined
      }
    >
      {!view.readable ? (
        <p
          data-testid="gitops-recent-syncs-unreadable"
          class="py-4 text-center text-xs text-text-muted"
        >
          The GitOps endpoint returned a result this card cannot read.
        </p>
      ) : view.total === 0 ? (
        <p
          data-testid="gitops-recent-syncs-none"
          class="py-4 text-center text-xs text-text-muted"
        >
          GitOps is installed but no application you can see is managed yet.
        </p>
      ) : view.rows.length === 0 ? (
        // Applications exist and none of them reports a sync time. Distinct
        // from an empty fleet: something is configured and nothing has
        // reconciled, which is a finding rather than a blank.
        <p
          data-testid="gitops-recent-syncs-never"
          class="py-4 text-center text-xs text-text-muted"
        >
          No application you can see reports a sync yet.
        </p>
      ) : (
        <ul class="flex flex-col gap-2" data-testid="gitops-recent-syncs-list">
          {view.rows.map((row) => (
            <SyncItem key={row.id} row={row} />
          ))}
        </ul>
      )}

      {view.readable && view.untimed > 0 && view.rows.length > 0 && (
        <p
          data-testid="gitops-recent-syncs-untimed"
          class="mt-2 text-[11px] leading-snug text-text-muted"
        >
          {view.untimed} application{view.untimed === 1 ? "" : "s"} report
          {view.untimed === 1 ? "s" : ""} no sync time and{" "}
          {view.untimed === 1 ? "is" : "are"} not ranked here.
        </p>
      )}

      {view.unreadableRows > 0 && (
        <p
          data-testid="gitops-recent-syncs-unreadable-rows"
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
 * One sync.
 *
 * The revision is rendered in a monospaced face because it is an identifier an
 * operator compares against `git log`, and it is omitted entirely when the
 * payload carried none -- an em dash standing in for a sha is a worse read
 * than a shorter line.
 */
function SyncItem({ row }: { row: RecentSyncRow }) {
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
        <span class="shrink-0">
          <SyncStatusBadge status={row.syncStatus as SyncStatus} />
        </span>
      </span>
      <span class="flex items-center gap-1.5 truncate text-[10px] text-text-muted">
        <ToolBadge tool={row.tool} />
        <time dateTime={row.syncedAt}>{timeAgo(row.syncedAt)}</time>
        {row.revision !== "" && (
          <span class="truncate font-mono">{row.revision}</span>
        )}
      </span>
    </li>
  );
}

registerWidget({
  id: "gitops-recent-syncs",
  title: "Recent Syncs",
  family: "delivery",
  scopes: ["overview"],
  // The same single read as `gitops-app-health`. Two widgets declaring one key
  // is one request: the cache fetches each key at most once per cycle.
  //
  // `/v1/gitops/commits` is deliberately absent. It requires a repository URL
  // and a set of shas, and answers with a neutral empty shape when no Git
  // provider token is configured -- so it is an enhancement layered on top of
  // a row, never the row's source. Declaring it here would also make a card
  // that must be useful without it depend on it to render at all.
  sources: ["gitops-applications"],
  // Same reasoning as its sibling: an absent tool and a quiet one send the
  // same empty result (R1, KTD1).
  familyStatus: "gitops-status",
  // Pinned on five sides (KTD7). Four columns: a row carries an application
  // name, a tool badge, a relative time and a revision.
  minW: 4,
  minH: 3,
  defaultW: 4,
  defaultH: 5,
  modes: ["normal"],
  render: () => <GitOpsRecentSyncs />,
});
