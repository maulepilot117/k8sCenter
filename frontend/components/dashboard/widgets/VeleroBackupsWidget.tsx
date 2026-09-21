import StatusBadge from "@/components/ui/glass/StatusBadge.tsx";
import { SeverityCount } from "@/components/ui/ScanBadges.tsx";
import WidgetShell from "@/components/ui/WidgetShell.tsx";
import { phaseTone } from "@/components/velero/velero-utils.ts";
import { dashboardData } from "@/lib/dashboard/data.ts";
// The outcome ranking, the worst-first roll-up and the unreadable-row
// accounting live in lib/, under test, because none of them is a field read
// (D-10, KTD8). Do not inline them.
import type { BackupRow } from "@/lib/dashboard/expiry.ts";
import { BACKUPS_PAGE_HREF, backupsView } from "@/lib/dashboard/expiry.ts";
import { registerWidget } from "@/lib/dashboard/registry.ts";
import { age } from "@/lib/format.ts";

/** How many backups the card lists. The counts above them cover every backup
 * the endpoint returned, not just these. */
const ROW_LIMIT = 5;

/** What the card leads with, per worst outcome present. `completed` and the
 * no-backups case are handled as copy rather than as a colour. */
const WORST_COLORS: Record<string, string> = {
  failed: "var(--error)",
  inProgress: "var(--accent)",
  other: "var(--text-muted)",
  completed: "var(--success)",
};

/**
 * Whether the cluster's Velero backups are actually completing.
 *
 * A roll-up by outcome, ranked rather than counted: a cluster whose last
 * backup failed while another is running is a cluster with a failed backup,
 * and a card that led with "in progress" would read as reassurance. The
 * ordering lives in `BACKUP_OUTCOMES` and the phase-to-outcome mapping reuses
 * `getPhaseCategory` -- the same function the backups, restores and schedules
 * tables colour their rows with -- so the dashboard and the backups page never
 * disagree about the same row.
 *
 * Absence and a quiet backup schedule are opposite readings of the same
 * response. Velero is CRD-discovered and `/v1/velero/backups` answers 200 with
 * an empty array whether Velero is absent, the account cannot list backups, or
 * nobody has taken one. The declared `velero-status` family separates the
 * first from the rest, resolved by the shell before `render` is called (R1,
 * KTD1) -- and the empty case then says "Velero is installed and has no
 * backups", which is a warning rather than good news.
 */
function VeleroBackups() {
  const view = backupsView(
    dashboardData.state("velero-backups-list").data,
    ROW_LIMIT,
  );
  const attention =
    view.counts.failed + view.counts.inProgress + view.counts.other;

  return (
    <WidgetShell
      title="Backups"
      action={
        view.readable && view.worst !== null ? (
          <span
            data-testid="velero-backups-summary"
            class="text-xs"
            style={{ color: WORST_COLORS[view.worst] ?? "var(--text-muted)" }}
          >
            {view.total} backup{view.total === 1 ? "" : "s"}
          </span>
        ) : undefined
      }
    >
      {!view.readable ? (
        // The backups route answered with something this build cannot read. An
        // empty list here would read as a cluster with nothing failing.
        <p
          data-testid="velero-backups-unreadable"
          class="py-4 text-center text-xs text-text-muted"
        >
          The backups endpoint returned a result this card cannot read.
        </p>
      ) : view.total === 0 ? (
        // Velero is installed -- the family status said so -- and has taken
        // nothing. Not an absence, and emphatically not a clean bill of health:
        // a cluster with a backup operator and no backups is the case this
        // card exists to surface.
        <p
          data-testid="velero-backups-none"
          class="py-4 text-center text-xs text-text-muted"
        >
          Velero is installed but no backup you can see has been taken.
        </p>
      ) : (
        <>
          <div
            class="mb-3 flex flex-wrap gap-1.5"
            data-testid="velero-backups-counts"
          >
            <SeverityCount
              label="failed"
              count={view.counts.failed}
              color="var(--error)"
            />
            <SeverityCount
              label="running"
              count={view.counts.inProgress}
              color="var(--accent)"
            />
            <SeverityCount
              label="unknown"
              count={view.counts.other}
              color="var(--text-muted)"
            />
            <SeverityCount
              label="completed"
              count={view.counts.completed}
              color="var(--success)"
            />
          </div>

          {attention === 0 ? (
            <p
              data-testid="velero-backups-clear"
              class="py-2 text-center text-xs text-text-muted"
            >
              Every backup you can see completed
              {view.lastCompleted === ""
                ? "."
                : `; the most recent started ${age(view.lastCompleted)} ago.`}
            </p>
          ) : (
            <ul class="flex flex-col gap-2" data-testid="velero-backups-list">
              {view.rows.map((row) => (
                <BackupItem key={`${row.namespace}/${row.name}`} row={row} />
              ))}
            </ul>
          )}

          {attention > view.rows.length && (
            <p
              data-testid="velero-backups-more"
              class="mt-2 text-[11px] leading-snug text-text-muted"
            >
              Showing the {view.rows.length} worst of {attention} that did not
              complete.
            </p>
          )}
        </>
      )}

      {view.unreadableRows > 0 && (
        <p
          data-testid="velero-backups-unreadable-rows"
          class="mt-2 text-[11px] leading-snug text-text-muted"
        >
          {view.unreadableRows} entr
          {view.unreadableRows === 1 ? "y" : "ies"} named no backup this card
          could render and {view.unreadableRows === 1 ? "was" : "were"} left
          out.
        </p>
      )}

      <a
        href={BACKUPS_PAGE_HREF}
        class="mt-3 block text-xs text-accent no-underline"
      >
        View all backups →
      </a>
    </WidgetShell>
  );
}

/**
 * One backup's row. Not a link: Velero has no per-backup route -- the backups
 * page opens a drawer over its table -- so the card links to the list and
 * leaves the row as text rather than inventing a URL that would 404.
 *
 * `phaseTone` is the backups table's own phase colouring, reused rather than
 * restyled.
 */
function BackupItem({ row }: { row: BackupRow }) {
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
            label={row.phase === "" ? "unknown" : row.phase}
            tone={phaseTone(row.phase)}
          />
        </span>
      </span>
      <span class="truncate text-[10px] text-text-muted">
        {row.startTime === "" ? "not started" : `${age(row.startTime)} ago`}
        {row.errors > 0
          ? ` · ${row.errors} error${row.errors === 1 ? "" : "s"}`
          : ""}
        {row.warnings > 0
          ? ` · ${row.warnings} warning${row.warnings === 1 ? "" : "s"}`
          : ""}
      </span>
    </li>
  );
}

registerWidget({
  id: "velero-backups",
  title: "Backups",
  family: "data-protection",
  scopes: ["overview"],
  sources: ["velero-backups-list"],
  // Velero is CRD-discovered, so an absent operator and a cluster nobody has
  // backed up produce the same 200 with the same empty array. Without this the
  // card would report a cluster with no backup tooling at all as one with
  // nothing failing (R1, KTD1).
  familyStatus: "velero-status",
  // Pinned on five sides -- here, two literals in registry_test.ts, the Go
  // catalog and two literals in parity_test.go (KTD7). Chosen once. Wider than
  // the three-column minimum: a Velero backup name is routinely
  // `daily-full-20260920010000`, beside a phase badge.
  minW: 4,
  minH: 3,
  defaultW: 4,
  defaultH: 5,
  modes: ["normal"],
  render: () => <VeleroBackups />,
});
