import WidgetShell from "@/components/ui/WidgetShell.tsx";
import { dashboardData } from "@/lib/dashboard/data.ts";
import { registerWidget } from "@/lib/dashboard/registry.ts";
import type {
  ResourceCounts,
  ResourceListPage,
} from "@/lib/dashboard/wire-types.ts";
// The two-payload fold, the readiness rule and the unknown/pending/counted
// distinction all live in lib/, under test, because none of them is a field
// read and this repo has no component test harness (D-10, KTD8). Do not
// inline them.
import type { WorkloadKindHealth } from "@/lib/dashboard/workload-health.ts";
import {
  rollUpWorkloadHealth,
  WORKLOAD_KIND_HREFS,
  WORKLOAD_KIND_LABELS,
} from "@/lib/dashboard/workload-health.ts";

/**
 * Controller health: Deployments, StatefulSets and DaemonSets split into
 * ready and degraded, one row each, each row linking to its own page (R7).
 *
 * Four sources, which needs justifying. `/v1/resources/counts` is required and
 * the three list reads are optional, and the asymmetry is the widget's whole
 * design:
 *
 * - Counts is the only route that answers "may this account list this kind at
 *   all". It OMITS a kind the caller cannot list rather than zeroing it, so
 *   its silence is a fact about the account. That is what lets a row say
 *   "not visible" instead of printing a reassuring 0/0.
 * - Counts is also the only honest total. The list route caps a page at 500
 *   items, so on a large cluster `items.length` is a sample; counts is the
 *   population. A row that reported the sample size as the total would shrink
 *   a 3000-Deployment cluster to 500 without saying so.
 * - The lists are optional so one kind's failure costs one row rather than the
 *   card. A user who may list Deployments but not DaemonSets gets two real
 *   rows and one honest blank, where a required list would blank all three
 *   with a permission card.
 *
 * Counts being required is also what keeps the card from rendering before it
 * knows anything: a widget whose every source is optional resolves to `ready`
 * immediately (see `resolveWidgetState`), and three "unknown" rows on first
 * paint would claim invisibility the payload has not reported yet.
 *
 * The route is local-cluster only -- it refuses a remote cluster context with
 * a 400 -- so on a remote cluster this required source fails and WidgetHost
 * renders the error state. That is the intended outcome: a card of zeros
 * about a cluster whose workloads were never read is the failure mode this
 * release exists to remove.
 */
function WorkloadHealth() {
  const counts = dashboardData.state<ResourceCounts>("resource-counts").data;
  const roll = rollUpWorkloadHealth(counts, {
    deployments:
      dashboardData.state<ResourceListPage>("deployments-list").data?.items ??
      null,
    statefulsets:
      dashboardData.state<ResourceListPage>("statefulsets-list").data?.items ??
      null,
    daemonsets:
      dashboardData.state<ResourceListPage>("daemonsets-list").data?.items ??
      null,
  });

  return (
    <WidgetShell
      title="Workload Health"
      action={
        roll.ready + roll.degraded > 0 ? (
          <span
            data-testid="workload-health-summary"
            class="text-xs"
            style={{
              color: roll.degraded > 0 ? "var(--warning)" : "var(--success)",
            }}
          >
            {roll.ready} ready
            {roll.degraded > 0 ? ` · ${roll.degraded} degraded` : ""}
          </span>
        ) : undefined
      }
    >
      {roll.empty ? (
        <p
          data-testid="workload-health-empty"
          class="py-4 text-center text-xs text-text-muted"
        >
          No Deployments, StatefulSets or DaemonSets on this cluster.
        </p>
      ) : (
        <div class="flex flex-col gap-2" data-testid="workload-health-rows">
          {roll.kinds.map((k) => (
            <KindRow key={k.kind} kind={k} />
          ))}
        </div>
      )}

      {roll.unknownKinds.length > 0 && (
        <p
          data-testid="workload-health-unknown"
          class="mt-3 text-[11px] leading-snug text-text-muted"
        >
          {roll.unknownKinds.map((k) => WORKLOAD_KIND_LABELS[k]).join(", ")}
          {roll.unknownKinds.length === 1 ? " is" : " are"} not visible to this
          account, so {roll.unknownKinds.length === 1 ? "it is" : "they are"}{" "}
          left out of the counts above.
        </p>
      )}

      {roll.truncated && (
        <p
          data-testid="workload-health-truncated"
          class="mt-2 text-[11px] leading-snug text-text-muted"
        >
          Readiness covers the first page of each kind; the totals are complete.
        </p>
      )}
    </WidgetShell>
  );
}

/** One kind's row. Three renderings, one per visibility, because the three
 * say genuinely different things and a shared bar with a blank number would
 * read as zero in two of them. */
function KindRow({ kind }: { kind: WorkloadKindHealth }) {
  const label = WORKLOAD_KIND_LABELS[kind.kind];
  const pct =
    kind.counted > 0 ? Math.round((kind.ready / kind.counted) * 100) : 0;

  return (
    <div class="flex items-center gap-3 text-xs">
      <a
        href={WORKLOAD_KIND_HREFS[kind.kind]}
        class="w-24 shrink-0 truncate font-semibold text-text-primary no-underline hover:text-accent"
      >
        {label}
      </a>

      {kind.visibility === "unknown" ? (
        <span class="flex-1 text-[11px] italic text-text-muted">
          Not visible to this account
        </span>
      ) : kind.visibility === "pending" ? (
        <span class="flex-1 text-[11px] text-text-muted">
          {kind.total} total · checking readiness…
        </span>
      ) : (
        <>
          <span
            aria-hidden="true"
            class="h-2 flex-1 overflow-hidden rounded-sm bg-hover"
          >
            <span
              class="block h-full rounded-sm"
              style={{
                width: `${kind.counted > 0 ? pct : 0}%`,
                background:
                  kind.degraded > 0 ? "var(--warning)" : "var(--success)",
              }}
            />
          </span>
          <span class="w-20 shrink-0 text-right font-mono text-text-secondary">
            {kind.ready}/{kind.counted}
            {kind.truncated ? ` of ${kind.total}` : ""}
          </span>
        </>
      )}
    </div>
  );
}

registerWidget({
  id: "workload-health",
  title: "Workload Health",
  family: "workloads",
  scopes: ["overview"],
  sources: [
    "resource-counts",
    "deployments-list",
    "statefulsets-list",
    "daemonsets-list",
  ],
  // See the component docstring: the three lists are optional so one kind's
  // refusal costs one row rather than the card, and counts stays required so
  // the card never paints three "not visible" rows before anything has
  // answered.
  optionalSources: ["deployments-list", "statefulsets-list", "daemonsets-list"],
  // No `familyStatus`. None of these three kinds is CRD-discovered -- they are
  // core `apps/v1` and present on every cluster -- so there is no discovery
  // route to ask and nothing for the "not installed" state to be true of.
  // Per-kind invisibility is an RBAC fact, and counts reports it directly.
  //
  // Pinned on five sides -- here, two literals in registry_test.ts, the Go
  // catalog and two literals in parity_test.go (KTD7). Chosen once.
  minW: 3,
  minH: 3,
  defaultW: 4,
  defaultH: 5,
  modes: ["normal"],
  render: () => <WorkloadHealth />,
});
