import type { Tone } from "@/components/ui/glass/StatusBadge.tsx";
import StatusBadge from "@/components/ui/glass/StatusBadge.tsx";
import { SeverityCount } from "@/components/ui/ScanBadges.tsx";
import WidgetShell from "@/components/ui/WidgetShell.tsx";
import { dashboardData } from "@/lib/dashboard/data.ts";
// The result roll-up, the target composition and the unreadable-row
// accounting live in lib/, under test (D-10, KTD8). Do not inline them.
import type { AuditRow } from "@/lib/dashboard/platform.ts";
import {
  AUDIT_PAGE_HREF,
  auditActivityView,
} from "@/lib/dashboard/platform.ts";
import { registerWidget } from "@/lib/dashboard/registry.ts";
import { age } from "@/lib/format.ts";

/** How many entries the card lists. The counts above them cover the page the
 * route returned, which is all this card asked for. */
const ROW_LIMIT = 5;

/** `audit.Result`, coloured. An unrecognised value is muted rather than
 * warned: a result this build does not know is a gap in what we can say. */
const RESULT_TONE: Record<string, Tone> = {
  success: "ok",
  failure: "crit",
  denied: "warn",
};

/**
 * Who changed what, most recent first.
 *
 * The page's own order, not a ranking: the handler sorts by timestamp
 * descending and the audit page renders that, so promoting denied entries here
 * would make the two surfaces disagree about the same rows. The counts above
 * the list are what surfaces a refusal that would otherwise be three rows
 * down.
 *
 * `/v1/audit/logs` is gated by `middleware.RequireAdmin`, so for most accounts
 * the permission state IS this card's normal rendering -- resolved by the
 * shell from the route's 403, with no retry affordance, before `render` is
 * ever called (R2). The palette marks the entry before it is added, from the
 * session's own roles rather than from anything fetched.
 *
 * The other absence is the deployment's: without a database the audit logger
 * is the slog one, which is not `audit.Queryable`, and the handler answers
 * 503. `ABSENT_STATUSES` maps that onto the unavailable state -- an admin on
 * such a deployment is told the log is not kept, rather than that it could not
 * be loaded.
 */
function AuditActivity() {
  const view = auditActivityView(
    dashboardData.state("audit-log").data,
    ROW_LIMIT,
  );
  const attention = view.counts.failure + view.counts.denied;

  return (
    <WidgetShell
      title="Audit Activity"
      action={
        view.readable && view.total > 0 ? (
          <span
            data-testid="audit-activity-summary"
            class="text-xs"
            style={{
              color: attention > 0 ? "var(--warning)" : "var(--text-muted)",
            }}
          >
            last {view.total}
          </span>
        ) : undefined
      }
    >
      {!view.readable ? (
        // The log answered with something this build cannot read. An empty
        // list here would read as a cluster nobody has changed.
        <p
          data-testid="audit-activity-unreadable"
          class="py-4 text-center text-xs text-text-muted"
        >
          The audit endpoint returned a result this card cannot read.
        </p>
      ) : view.total === 0 ? (
        <p
          data-testid="audit-activity-none"
          class="py-4 text-center text-xs text-text-muted"
        >
          Audit logging is on and has recorded nothing yet.
        </p>
      ) : (
        <>
          <div
            class="mb-3 flex flex-wrap gap-1.5"
            data-testid="audit-activity-counts"
          >
            <SeverityCount
              label="failed"
              count={view.counts.failure}
              color="var(--error)"
            />
            <SeverityCount
              label="denied"
              count={view.counts.denied}
              color="var(--warning)"
            />
            <SeverityCount
              label="unknown"
              count={view.counts.other}
              color="var(--text-muted)"
            />
            <SeverityCount
              label="succeeded"
              count={view.counts.success}
              color="var(--success)"
            />
          </div>

          <ul class="flex flex-col gap-2" data-testid="audit-activity-list">
            {view.rows.map((row, idx) => (
              <AuditItem
                key={`${row.timestamp}-${row.user}-${idx}`}
                row={row}
              />
            ))}
          </ul>

          {view.total > view.rows.length && (
            <p
              data-testid="audit-activity-more"
              class="mt-2 text-[11px] leading-snug text-text-muted"
            >
              Showing the {view.rows.length} most recent of the {view.total}{" "}
              this card read.
            </p>
          )}
        </>
      )}

      {view.unreadableRows > 0 && (
        <p
          data-testid="audit-activity-unreadable-rows"
          class="mt-2 text-[11px] leading-snug text-text-muted"
        >
          {view.unreadableRows} entr
          {view.unreadableRows === 1 ? "y" : "ies"} named no action this card
          could render and {view.unreadableRows === 1 ? "was" : "were"} left
          out.
        </p>
      )}

      <a
        href={AUDIT_PAGE_HREF}
        class="mt-3 block text-xs text-accent no-underline"
      >
        View the audit log →
      </a>
    </WidgetShell>
  );
}

/**
 * One audited action. Not a link to the object: the entry records what the
 * object was called at the time, and the object may since have been deleted --
 * which is exactly what a `delete` row says. A link that 404s on the most
 * interesting rows is worse than text.
 */
function AuditItem({ row }: { row: AuditRow }) {
  return (
    <li class="flex flex-col gap-1">
      <span class="flex items-center justify-between gap-2">
        <span class="min-w-0 truncate text-xs text-text-secondary">
          <span class="font-medium">
            {row.user === "" ? "unknown" : row.user}
          </span>{" "}
          {row.action}
          {row.target !== "" && (
            <span class="font-mono text-[11px] text-text-muted">
              {" "}
              {row.target}
            </span>
          )}
        </span>
        <span class="shrink-0">
          <StatusBadge
            label={row.result === "" ? "unknown" : row.result}
            tone={RESULT_TONE[row.result] ?? "neutral"}
          />
        </span>
      </span>
      <span class="truncate text-[10px] text-text-muted">
        {row.timestamp === "" ? "no timestamp" : `${age(row.timestamp)} ago`}
        {row.detail === "" ? "" : ` · ${row.detail}`}
      </span>
    </li>
  );
}

registerWidget({
  id: "audit-activity",
  title: "Audit Activity",
  family: "platform",
  scopes: ["overview"],
  sources: ["audit-log"],
  // `/v1/audit/logs` is gated by `middleware.RequireAdmin`, so for most
  // accounts this card's steady state is the permission one. Declaring it here
  // is what lets the palette say so BEFORE the widget is added (R3); the
  // rendering itself needs nothing, because the route's 403 already resolves
  // to the permission state through the failure classifier.
  //
  // No `familyStatus`: audit logging is not CRD-discovered. Its absence is the
  // deployment's missing database, which the handler reports as 503 -- see
  // `ABSENT_STATUSES` in lib/dashboard/types.ts.
  adminOnly: true,
  // Pinned on five sides -- here, two literals in registry_test.ts, the Go
  // catalog and two literals in parity_test.go (KTD7). Five columns rather
  // than four: a row is a user, a verb and a `kind/namespace/name` triple on
  // one line, which is the longest row in this catalog.
  minW: 5,
  minH: 3,
  defaultW: 5,
  defaultH: 5,
  modes: ["normal"],
  render: () => <AuditActivity />,
});
