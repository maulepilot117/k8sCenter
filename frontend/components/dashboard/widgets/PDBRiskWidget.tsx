import WidgetShell from "@/components/ui/WidgetShell.tsx";
// The blocked/no-pods/unknown distinction lives in lib/, under test, because
// telling a budget that blocks a drain apart from one that merely covers
// nothing is not a field read (D-10, KTD8). Do not inline it.
import type { PDBState, PDBStatus } from "@/lib/dashboard/autoscaling.ts";
import {
  PDB_LIST_HREF,
  pdbHref,
  pdbRiskView,
} from "@/lib/dashboard/autoscaling.ts";
import { dashboardData } from "@/lib/dashboard/data.ts";
import { registerWidget } from "@/lib/dashboard/registry.ts";
import type { ResourceListPage } from "@/lib/dashboard/wire-types.ts";

/** How many rows the card lists. The header counts cover the whole page. */
const ROW_LIMIT = 6;

const STATE_COLOR: Readonly<Record<PDBState, string>> = {
  blocked: "var(--error)",
  unknown: "var(--text-muted)",
  "no-pods": "var(--text-muted)",
  healthy: "var(--success)",
};

const STATE_BADGE: Readonly<Record<PDBState, string>> = {
  blocked: "blocks drain",
  unknown: "unreadable",
  "no-pods": "no pods",
  healthy: "",
};

/**
 * PodDisruptionBudgets that would stop a node drain.
 *
 * The finding is `disruptionsAllowed: 0` on a budget that actually covers
 * pods: the eviction API refuses every one of them, so a `kubectl drain` on
 * any node running them hangs until somebody notices. An operator who wants a
 * node back does not learn this from the node page.
 *
 * The card deliberately does NOT list every budget on the cluster. A healthy
 * one has nothing for anyone to do, and listing all of them turns a findings
 * card into an inventory that happens to be sorted; the header counts still
 * report them. The one exception to "zero allowed is a finding" is a budget
 * whose selector matches nothing, which is informational rather than a risk —
 * it allows no disruption because there is nothing to disrupt, and filing it
 * as a drain blocker would raise a false alarm on every budget whose workload
 * was removed. That distinction is `pdbRiskView`'s, not this file's.
 */
function PDBRisk() {
  const page = dashboardData.state<ResourceListPage>("pdbs-list").data;
  const view = pdbRiskView(page, ROW_LIMIT);

  return (
    <WidgetShell
      title="Disruption Budgets"
      action={
        view.blocked > 0 ? (
          <span
            data-testid="pdb-risk-summary"
            class="text-xs"
            style={{ color: "var(--error)" }}
          >
            {view.blocked} blocking
          </span>
        ) : view.healthy > 0 ? (
          <span data-testid="pdb-risk-summary" class="text-xs text-text-muted">
            {view.healthy} allow disruption
          </span>
        ) : undefined
      }
    >
      {!view.readable ? (
        // The list route answered with something this build cannot read. The
        // empty state below counts zero because there was nothing to count,
        // not because the cluster is clean, and saying the latter over an
        // unanswered read is the failure this release exists to prevent.
        <p
          data-testid="pdb-risk-unreadable-page"
          class="py-4 text-center text-xs text-text-muted"
        >
          The budget roll-up returned a result this card cannot read.
        </p>
      ) : view.pdbs.length === 0 ? (
        <p
          data-testid="pdb-risk-empty"
          class="py-4 text-center text-xs text-text-muted"
        >
          {view.total === 0
            ? "No PodDisruptionBudgets on this cluster."
            : `All ${view.healthy} budget${view.healthy === 1 ? "" : "s"} allow at least one disruption.`}
        </p>
      ) : (
        <ul class="flex flex-col gap-1.5" data-testid="pdb-risk-list">
          {view.pdbs.map((p) => (
            <PDBRow key={`${p.namespace}/${p.name}`} pdb={p} />
          ))}
        </ul>
      )}

      {view.truncated && (
        <p
          data-testid="pdb-risk-truncated"
          class="mt-2 text-[11px] leading-snug text-text-muted"
        >
          Ranked across the first {view.counted} of {view.total} budgets.
        </p>
      )}

      <a
        href={PDB_LIST_HREF}
        class="mt-3 block text-xs text-accent no-underline"
      >
        View all budgets →
      </a>
    </WidgetShell>
  );
}

/**
 * One budget's row.
 *
 * The right-hand figure is `currentHealthy/desiredHealthy`, which is what
 * decides the allowance: a budget allows a disruption exactly when healthy
 * pods outnumber the minimum it insists on. Printing the allowance alone
 * would say the budget is blocking without saying by how much.
 */
function PDBRow({ pdb }: { pdb: PDBStatus }) {
  const badge = STATE_BADGE[pdb.state];
  const healthy =
    pdb.currentHealthy === null || pdb.desiredHealthy === null
      ? "—"
      : `${pdb.currentHealthy}/${pdb.desiredHealthy}`;

  return (
    <li class="flex items-center justify-between gap-2 text-xs">
      <span class="flex min-w-0 items-center gap-2">
        <span
          aria-hidden="true"
          class="size-2 shrink-0 rounded-full"
          style={{ background: STATE_COLOR[pdb.state] }}
        />
        <a
          href={pdbHref(pdb.namespace, pdb.name)}
          title={`${pdb.namespace}/${pdb.name}`}
          class="truncate text-text-secondary no-underline hover:text-accent"
        >
          {pdb.name}
        </a>
        {badge !== "" && (
          <span
            class="shrink-0 rounded-md px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-wide"
            style={{
              background:
                pdb.state === "blocked"
                  ? "var(--error-dim)"
                  : "var(--bg-hover)",
              color: STATE_COLOR[pdb.state],
            }}
          >
            {badge}
          </span>
        )}
      </span>
      <span
        class="shrink-0 font-mono font-semibold"
        style={{ color: STATE_COLOR[pdb.state] }}
        title="healthy pods / minimum the budget insists on"
      >
        {healthy}
      </span>
    </li>
  );
}

registerWidget({
  id: "pdb-risk",
  title: "Disruption Budgets",
  family: "workloads",
  scopes: ["overview"],
  sources: ["pdbs-list"],
  // No `familyStatus`. PodDisruptionBudget is core `policy/v1`, not a CRD, so
  // there is no discovery route to ask. A cluster with no budgets renders the
  // empty copy, which says so.
  //
  // Pinned on five sides -- here, two literals in registry_test.ts, the Go
  // catalog and two literals in parity_test.go (KTD7). Chosen once.
  minW: 3,
  minH: 3,
  defaultW: 4,
  defaultH: 5,
  modes: ["normal"],
  render: () => <PDBRisk />,
});
