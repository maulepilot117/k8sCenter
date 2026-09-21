import WidgetShell from "@/components/ui/WidgetShell.tsx";
import { dashboardData } from "@/lib/dashboard/data.ts";
// The ranking, the no-quota exclusion and the band arithmetic live in lib/,
// under test, because none of them is a field read (D-10, KTD8). Do not
// inline them.
import type {
  PressureLevel,
  QuotaPressureRow,
} from "@/lib/dashboard/pressure.ts";
import {
  namespaceHref,
  QUOTA_PAGE_HREF,
  quotaPressureView,
} from "@/lib/dashboard/pressure.ts";
import { registerWidget } from "@/lib/dashboard/registry.ts";

/** How many rows the card lists. The header counts cover every quota'd
 * namespace in the payload, not just these. */
const ROW_LIMIT = 6;

const LEVEL_COLOR: Readonly<Record<PressureLevel, string>> = {
  critical: "var(--error)",
  warning: "var(--warning)",
  ok: "var(--success)",
};

/**
 * The namespaces closest to running out of quota.
 *
 * Ranked by PROPORTION of quota used, not by how much of anything is used: a
 * two-pod namespace at 92% of its quota is about to start having admissions
 * refused, and the busiest namespace on the cluster at 30% is not. The
 * proportion is taken across whichever resources each quota actually
 * constrains -- pods, CPU, memory, services, secrets, any of the two dozen
 * things a ResourceQuota can cap -- because the tightest dimension is the one
 * that will refuse the next object, whatever it happens to be.
 *
 * A namespace with no quota is NOT on this card. The limits route returns it,
 * because it lists any namespace carrying a quota or a LimitRange, and putting
 * it at the bottom of a pressure ranking would assert it is comfortably inside
 * a limit it does not have. The footer reports how many there are instead,
 * which is the more useful fact: an ungoverned namespace has no ceiling at
 * all.
 */
function QuotaPressure() {
  const view = quotaPressureView(
    dashboardData.state("limits-namespaces").data,
    ROW_LIMIT,
  );

  return (
    <WidgetShell
      title="Quota Pressure"
      action={
        view.atRisk > 0 ? (
          <span
            data-testid="quota-pressure-summary"
            class="text-xs"
            style={{ color: "var(--warning)" }}
          >
            {view.atRisk} near limit
          </span>
        ) : view.quotaed > 0 ? (
          <span
            data-testid="quota-pressure-summary"
            class="text-xs text-text-muted"
          >
            {view.quotaed} with quota
          </span>
        ) : undefined
      }
    >
      {!view.readable ? (
        // The limits route answered with something this build cannot read.
        // Saying so is the honest rendering; an empty list here would read as
        // a cluster where no namespace is near its quota.
        <p
          data-testid="quota-pressure-unreadable"
          class="py-4 text-center text-xs text-text-muted"
        >
          The quota roll-up returned a result this card cannot read.
        </p>
      ) : view.rows.length === 0 ? (
        <p
          data-testid="quota-pressure-empty"
          class="py-4 text-center text-xs text-text-muted"
        >
          {view.quotaed === 0
            ? "No namespace on this cluster has a ResourceQuota."
            : `All ${view.quotaed} quota${view.quotaed === 1 ? "" : "s"} have room.`}
        </p>
      ) : (
        <ul class="flex flex-col gap-2" data-testid="quota-pressure-list">
          {view.rows.map((row) => (
            <QuotaRow key={row.namespace} row={row} />
          ))}
        </ul>
      )}

      {view.unquotaed > 0 && (
        <p
          data-testid="quota-pressure-unquotaed"
          class="mt-2 text-[11px] leading-snug text-text-muted"
        >
          {view.unquotaed} namespace{view.unquotaed === 1 ? "" : "s"} here{" "}
          {view.unquotaed === 1 ? "has" : "have"} no quota, so{" "}
          {view.unquotaed === 1 ? "it is" : "they are"} not ranked.
        </p>
      )}

      {view.unreadableRows > 0 && (
        <p
          data-testid="quota-pressure-unreadable-rows"
          class="mt-2 text-[11px] leading-snug text-text-muted"
        >
          {view.unreadableRows} namespace
          {view.unreadableRows === 1 ? "" : "s"} reported no readable
          utilization and {view.unreadableRows === 1 ? "was" : "were"} left out.
        </p>
      )}

      <a
        href={QUOTA_PAGE_HREF}
        class="mt-3 block text-xs text-accent no-underline"
      >
        View namespace limits →
      </a>
    </WidgetShell>
  );
}

/**
 * One namespace's row.
 *
 * The bar is the headline proportion; the caption underneath names the CPU and
 * memory dimensions when the quota constrains them. A quota that says nothing
 * about CPU shows no CPU figure rather than a zero -- a namespace whose quota
 * does not mention CPU is not a namespace using none.
 */
function QuotaRow({ row }: { row: QuotaPressureRow }) {
  const color = LEVEL_COLOR[row.level];
  const dims: string[] = [];
  if (row.cpuPercent !== null) dims.push(`CPU ${Math.round(row.cpuPercent)}%`);
  if (row.memoryPercent !== null) {
    dims.push(`memory ${Math.round(row.memoryPercent)}%`);
  }

  return (
    <li class="flex flex-col gap-1">
      <span class="flex items-center justify-between gap-2 text-xs">
        <a
          href={namespaceHref(row.namespace)}
          title={row.namespace}
          class="min-w-0 truncate text-text-secondary no-underline hover:text-accent"
        >
          {row.namespace}
        </a>
        <span class="shrink-0 font-mono font-semibold" style={{ color }}>
          {Math.round(row.percent)}%
        </span>
      </span>
      <span
        aria-hidden="true"
        class="block h-1.5 w-full overflow-hidden rounded-sm bg-hover"
      >
        <span
          class="block h-full rounded-sm"
          style={{
            width: `${Math.max(0, Math.min(100, row.percent))}%`,
            background: color,
          }}
        />
      </span>
      <span class="text-[10px] text-text-muted">
        {dims.length > 0
          ? dims.join(" · ")
          : `${row.quotaCount} quota${row.quotaCount === 1 ? "" : "s"}, no CPU or memory limit`}
      </span>
    </li>
  );
}

registerWidget({
  id: "quota-pressure",
  title: "Quota Pressure",
  family: "reliability",
  scopes: ["overview"],
  sources: ["limits-namespaces"],
  // No `familyStatus`. ResourceQuota and LimitRange are core `v1`, not CRDs,
  // so there is no discovery route to ask and nothing to be absent. A cluster
  // with no quotas renders the empty copy, which says so in as many words.
  //
  // Pinned on five sides -- here, two literals in registry_test.ts, the Go
  // catalog and two literals in parity_test.go (KTD7). Chosen once.
  minW: 3,
  minH: 3,
  defaultW: 4,
  defaultH: 6,
  modes: ["normal"],
  render: () => <QuotaPressure />,
});
