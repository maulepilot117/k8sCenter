import { BlockingBadge, SeverityBadge } from "@/components/ui/PolicyBadges.tsx";
import { SeverityCount } from "@/components/ui/ScanBadges.tsx";
import WidgetShell from "@/components/ui/WidgetShell.tsx";
import { SEVERITY_COLORS } from "@/lib/badge-colors.ts";
import { dashboardData } from "@/lib/dashboard/data.ts";
import { registerWidget } from "@/lib/dashboard/registry.ts";
// The severity ranking, the tiebreak and the unreadable-row accounting live
// in lib/, under test, because none of them is a field read (D-10, KTD8). Do
// not inline them.
import type { ViolationRow } from "@/lib/dashboard/severity.ts";
import {
  policyViolationsView,
  VIOLATIONS_PAGE_HREF,
} from "@/lib/dashboard/severity.ts";

/** How many rows the card lists. The header counts cover every violation the
 * endpoint returned, not just these. */
const ROW_LIMIT = 6;

/**
 * What the cluster's policy engines are currently refusing or recording.
 *
 * The second acceptance example of this release lives on this card: an engine
 * installed with zero violations reads as CLEAR, not as unavailable. The
 * distinction is not something the payload can make -- `/v1/policies/violations`
 * answers 200 with an empty list whether Kyverno is absent or merely quiet --
 * so it is made one level up, by the declared family status, before `render`
 * is called (KTD1). By the time this function runs, an engine is known to be
 * present, which is what lets the empty case say "nothing is being refused"
 * and mean it.
 *
 * Ranked by severity rather than by recency. A violations feed sorted newest
 * first buries a critical admission refusal under a morning's worth of audited
 * label warnings, and the card is six rows tall.
 */
function PolicyViolations() {
  const view = policyViolationsView(
    dashboardData.state("policy-violations-list").data,
    ROW_LIMIT,
  );

  return (
    <WidgetShell
      title="Policy Violations"
      action={
        view.readable && view.total > 0 ? (
          <span
            data-testid="policy-violations-summary"
            class="text-xs"
            style={{
              color: view.blocking > 0 ? "var(--error)" : "var(--warning)",
            }}
          >
            {view.total} total
            {view.blocking > 0 ? ` · ${view.blocking} blocking` : ""}
          </span>
        ) : undefined
      }
    >
      {!view.readable ? (
        // The violations route answered with something this build cannot read.
        // An empty list here would read as a cluster with nothing in breach.
        <p
          data-testid="policy-violations-unreadable"
          class="py-4 text-center text-xs text-text-muted"
        >
          The violations endpoint returned a result this card cannot read.
        </p>
      ) : view.total === 0 ? (
        // CLEAR, not unavailable. See the component docstring: an engine is
        // known to be installed by the time this renders.
        <p
          data-testid="policy-violations-clear"
          class="py-4 text-center text-xs text-text-muted"
        >
          No policy violations. Every resource you can see satisfies the
          policies in force.
        </p>
      ) : (
        <>
          <div
            data-testid="policy-violations-severities"
            class="mb-3 flex flex-wrap gap-1.5"
          >
            {view.bySeverity.map((bucket) => (
              <SeverityCount
                key={bucket.severity}
                label={bucket.severity}
                count={bucket.count}
                color={SEVERITY_COLORS[bucket.severity] ?? "var(--text-muted)"}
              />
            ))}
          </div>

          <ul class="flex flex-col gap-2" data-testid="policy-violations-list">
            {view.rows.map((row) => (
              <ViolationItem
                key={`${row.policy}/${row.namespace}/${row.kind}/${row.name}`}
                row={row}
              />
            ))}
          </ul>

          {view.total > view.rows.length && (
            <p
              data-testid="policy-violations-more"
              class="mt-2 text-[11px] leading-snug text-text-muted"
            >
              Showing the {view.rows.length} most severe of {view.total}.
            </p>
          )}
        </>
      )}

      {view.unreadableRows > 0 && (
        <p
          data-testid="policy-violations-unreadable-rows"
          class="mt-2 text-[11px] leading-snug text-text-muted"
        >
          {view.unreadableRows} violation
          {view.unreadableRows === 1 ? "" : "s"} named no resource this card
          could render and {view.unreadableRows === 1 ? "was" : "were"} counted
          only.
        </p>
      )}

      <a
        href={VIOLATIONS_PAGE_HREF}
        class="mt-3 block text-xs text-accent no-underline"
      >
        View all violations →
      </a>
    </WidgetShell>
  );
}

/**
 * One violation's row.
 *
 * The badges are the shared policy ones rather than chips styled here:
 * `SeverityBadge` and `BlockingBadge` are what the violations page itself
 * renders, and a dashboard card that invented its own colours for "critical"
 * would be the same word in two colours on two pages.
 */
function ViolationItem({ row }: { row: ViolationRow }) {
  const scope = row.namespace === "" ? "cluster-scoped" : row.namespace;
  return (
    <li class="flex flex-col gap-1">
      <span class="flex items-center justify-between gap-2">
        <span
          class="min-w-0 truncate text-xs text-text-secondary"
          title={row.policy}
        >
          {row.policy === "" ? "unnamed policy" : row.policy}
        </span>
        <span class="flex shrink-0 items-center gap-1">
          <SeverityBadge severity={row.severity} />
          <BlockingBadge blocking={row.blocking} />
        </span>
      </span>
      <span
        class="truncate text-[10px] text-text-muted"
        title={`${row.kind} ${scope}/${row.name}`}
      >
        {row.kind === "" ? "resource" : row.kind} · {scope}/{row.name}
      </span>
    </li>
  );
}

registerWidget({
  id: "policy-violations",
  title: "Policy Violations",
  family: "security",
  scopes: ["overview"],
  sources: ["policy-violations-list"],
  // The declaration that makes "zero violations" mean zero violations. The
  // route returns 200 with an empty list whether no engine is installed or
  // every resource is compliant, so the family's own status route is the only
  // thing that can tell the two apart (R1, KTD1).
  familyStatus: "policies-status",
  // Pinned on five sides -- here, two literals in registry_test.ts, the Go
  // catalog and two literals in parity_test.go (KTD7). Chosen once.
  minW: 4,
  minH: 3,
  defaultW: 4,
  defaultH: 5,
  modes: ["normal"],
  render: () => <PolicyViolations />,
});
