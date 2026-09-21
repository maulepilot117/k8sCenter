import { useState } from "preact/hooks";
import WidgetShell from "@/components/ui/WidgetShell.tsx";
import { dashboardData } from "@/lib/dashboard/data.ts";
import { registerWidget } from "@/lib/dashboard/registry.ts";
// The vector parsing, the ranking and the unit arithmetic live in lib/, under
// test, because none of them is a field read (D-10, KTD8). The slug names live
// there too, so this file holds no route knowledge at all.
import type { ConsumerMetric } from "@/lib/dashboard/top-consumers.ts";
import {
  CONSUMER_LABEL,
  CONSUMER_METRICS,
  CONSUMER_SOURCE,
  CONSUMER_UNIT,
  consumerHref,
  formatConsumerValue,
  rankConsumers,
} from "@/lib/dashboard/top-consumers.ts";

/** How many rows the card lists. The slug's `topk` already caps the query at
 * ten server-side; this is the display cap, and the smaller of the two wins. */
const ROW_LIMIT = 8;

/**
 * The pods using the most CPU or memory on the cluster, right now.
 *
 * Two things about how it gets the numbers are the point of the card.
 *
 * It reads a NAMED, server-owned PromQL template from the slug registry
 * (`cluster/top-consumers-cpu` and `cluster/top-consumers-memory`), not the
 * raw `/monitoring/query` route. The raw routes are admin-gated, so a widget
 * built on them would be an admin-only card; the slug route runs a
 * SelfSubjectAccessReview for `list` on pods instead, which is a grant an
 * ordinary operator has. That is the case the slug registry was added for
 * (R15), and it is why this card works for a non-admin.
 *
 * And the CPU/memory switch is a DISPLAY choice, held in component state.
 * Storing it would mean either two catalog entries or a `params` declaration
 * — putting a view toggle into the saved layout, into the layout's revision
 * history and into the server's per-widget parameter validation, for
 * something that is a button. Both sources are declared and fetched; the
 * toggle picks which one is rendered, and the stored layout never changes.
 *
 * `top-consumers-memory` is optional so that the card a viewer is actually
 * looking at is not blanked by the other tab's read. The CPU source is
 * required, which is what gives the card its loading, error and — via the
 * 404 the slug route refuses with — permission states.
 */
function TopConsumers() {
  const [metric, setMetric] = useState<ConsumerMetric>("cpu");
  const state = dashboardData.state(CONSUMER_SOURCE[metric]);
  const view = rankConsumers(state.data, ROW_LIMIT);
  const max = view.rows.length > 0 ? view.rows[0].value : 0;

  return (
    <WidgetShell
      title="Top Consumers"
      action={
        <div
          class="flex items-center gap-1"
          role="group"
          aria-label="Metric shown"
          data-testid="top-consumers-toggle"
        >
          {CONSUMER_METRICS.map((m) => (
            <button
              key={m}
              type="button"
              aria-pressed={m === metric}
              onClick={() => setMetric(m)}
              class="rounded-md px-2 py-0.5 text-[11px] font-semibold no-underline"
              style={{
                background: m === metric ? "var(--bg-hover)" : "transparent",
                color:
                  m === metric ? "var(--text-primary)" : "var(--text-muted)",
                border: "none",
                cursor: "pointer",
              }}
            >
              {CONSUMER_LABEL[m]}
            </button>
          ))}
        </div>
      }
    >
      {state.data === null ? (
        // Only reachable for the memory tab: the CPU source is required, so
        // the shell has already resolved loading, error and permission before
        // this component runs. The memory read is optional and may still be
        // in flight, or may have failed on its own.
        <p
          data-testid="top-consumers-pending"
          class="py-4 text-center text-xs text-text-muted"
        >
          {state.error === null
            ? "Loading memory usage…"
            : "Memory usage could not be loaded."}
        </p>
      ) : !view.readable ? (
        // Prometheus answered with something this build cannot read. Saying
        // so is the honest rendering; an empty list here would read as a
        // cluster where nothing is using anything.
        <p
          data-testid="top-consumers-unreadable"
          class="py-4 text-center text-xs text-text-muted"
        >
          Prometheus returned a result this card cannot read.
        </p>
      ) : view.rows.length === 0 ? (
        <p
          data-testid="top-consumers-empty"
          class="py-4 text-center text-xs text-text-muted"
        >
          Prometheus reported no pod {CONSUMER_LABEL[metric].toLowerCase()}{" "}
          usage.
        </p>
      ) : (
        <ul class="flex flex-col gap-1.5" data-testid="top-consumers-list">
          {view.rows.map((row) => (
            <li
              key={`${row.namespace}/${row.pod}`}
              class="flex items-center gap-2 text-xs"
            >
              <a
                href={consumerHref(row)}
                title={`${row.namespace}/${row.pod}`}
                class="min-w-0 flex-1 truncate text-text-secondary no-underline hover:text-accent"
              >
                {row.pod}
                <span class="ml-1.5 text-[10px] text-text-muted">
                  {row.namespace}
                </span>
              </a>
              <span
                aria-hidden="true"
                class="hidden h-1.5 w-16 shrink-0 overflow-hidden rounded-sm bg-hover sm:block"
              >
                <span
                  class="block h-full rounded-sm"
                  style={{
                    width: `${max > 0 ? Math.round((row.value / max) * 100) : 0}%`,
                    background: "var(--accent)",
                  }}
                />
              </span>
              <span class="w-16 shrink-0 text-right font-mono font-semibold text-text-primary">
                {formatConsumerValue(metric, row.value)}
              </span>
            </li>
          ))}
        </ul>
      )}

      {view.dropped > 0 && (
        <p
          data-testid="top-consumers-dropped"
          class="mt-2 text-[11px] leading-snug text-text-muted"
        >
          {view.dropped} series carried no readable value and{" "}
          {view.dropped === 1 ? "was" : "were"} left out.
        </p>
      )}

      {view.warnings.length > 0 && (
        <p
          data-testid="top-consumers-warnings"
          class="mt-2 text-[11px] leading-snug"
          style={{ color: "var(--warning)" }}
          title={view.warnings.join("\n")}
        >
          Prometheus reported the result as incomplete.
        </p>
      )}

      <p class="mt-2 text-[11px] text-text-muted">
        {CONSUMER_LABEL[metric]} {CONSUMER_UNIT[metric]}, cluster-wide.
      </p>

      <a
        href="/workloads/pods"
        class="mt-2 block text-xs text-accent no-underline"
      >
        View all pods →
      </a>
    </WidgetShell>
  );
}

registerWidget({
  id: "top-consumers",
  title: "Top Consumers",
  family: "workloads",
  scopes: ["overview"],
  // Both slugs, because the toggle is a view choice rather than stored state
  // and either tab must be able to render without a round trip.
  sources: ["top-consumers-cpu", "top-consumers-memory"],
  // Memory is optional so that a failure on the tab nobody is looking at does
  // not blank the tab they are. CPU stays required: it is the default view,
  // and it is what gives the card its loading, error and permission states.
  optionalSources: ["top-consumers-memory"],
  // No `familyStatus`. Prometheus is not one of the six CRD-discovered
  // families, and the slug route answers 503 when it was never discovered —
  // which the shell renders as an error carrying that message, not as a card
  // of zeros.
  //
  // Wider than the other workload cards: each row carries a pod name, its
  // namespace and a value, and three columns inside three grid columns
  // truncate the names to uselessness.
  //
  // Pinned on five sides -- here, two literals in registry_test.ts, the Go
  // catalog and two literals in parity_test.go (KTD7). Chosen once.
  minW: 4,
  minH: 4,
  defaultW: 4,
  defaultH: 6,
  modes: ["normal"],
  render: () => <TopConsumers />,
});
