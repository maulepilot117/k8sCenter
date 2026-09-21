import WidgetShell from "@/components/ui/WidgetShell.tsx";
import { dashboardData } from "@/lib/dashboard/data.ts";
// The classification and the ranking live in lib/, under test, and are shared
// with pod-restarts so the two cards cannot disagree about the same pod
// (D-10, KTD8). Do not inline them.
import { pendingPodsView } from "@/lib/dashboard/pod-health.ts";
import { registerWidget } from "@/lib/dashboard/registry.ts";
import type { ResourceListPage } from "@/lib/dashboard/wire-types.ts";
import { durationShort } from "@/lib/format.ts";

/** How many rows the card lists. The counts in the header cover the whole
 * page regardless, so the cap shortens the list without shrinking the
 * finding. */
const ROW_LIMIT = 6;

/**
 * Pods that are not up yet, longest-waiting first, with the unschedulable
 * ones leading.
 *
 * The two states are separated because they need different people. An
 * unschedulable pod is waiting on capacity, a taint or a node selector and
 * will wait forever until somebody changes something; a merely pending one is
 * usually a container that is still starting and will come up on its own. A
 * card that lumped them together would put a deploy in progress next to a
 * cluster that has run out of room and give them the same weight.
 *
 * An empty list here is a real finding rather than an absence of data: the
 * pods list is this widget's only source and it is required, so a refusal
 * renders the permission state and a failure renders the error state. By the
 * time the body runs, "no pods are waiting" is something we actually know.
 */
function PendingPods() {
  const page = dashboardData.state<ResourceListPage>("pods-list").data;
  const view = pendingPodsView(page, ROW_LIMIT);
  const waiting = view.unschedulable + view.pending;

  return (
    <WidgetShell
      title="Pending Pods"
      action={
        waiting > 0 ? (
          <span
            data-testid="pending-pods-summary"
            class="text-xs"
            style={{
              color: view.unschedulable > 0 ? "var(--error)" : "var(--warning)",
            }}
          >
            {view.unschedulable > 0
              ? `${view.unschedulable} unschedulable`
              : `${view.pending} pending`}
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
          data-testid="pending-pods-unreadable-page"
          class="py-4 text-center text-xs text-text-muted"
        >
          The pod roll-up returned a result this card cannot read.
        </p>
      ) : waiting === 0 ? (
        <p
          data-testid="pending-pods-empty"
          class="py-4 text-center text-xs text-text-muted"
        >
          Every pod has been scheduled and started.
        </p>
      ) : (
        <ul class="flex flex-col gap-1.5" data-testid="pending-pods-list">
          {view.pods.map((p) => (
            <li
              key={`${p.namespace}/${p.name}`}
              class="flex items-center justify-between gap-2 text-xs"
            >
              <span class="flex min-w-0 items-center gap-2">
                <span
                  aria-hidden="true"
                  class="size-2 shrink-0 rounded-full"
                  style={{
                    background:
                      p.state === "unschedulable"
                        ? "var(--error)"
                        : "var(--warning)",
                  }}
                />
                <a
                  href={`/workloads/pods/${encodeURIComponent(p.namespace)}/${encodeURIComponent(p.name)}`}
                  title={`${p.namespace}/${p.name}`}
                  class="truncate text-text-secondary no-underline hover:text-accent"
                >
                  {p.name}
                </a>
              </span>
              <span class="flex shrink-0 items-center gap-2">
                {p.reason !== "" && (
                  <span class="max-w-[9rem] truncate text-[11px] text-text-muted">
                    {p.reason}
                  </span>
                )}
                <span class="font-mono text-text-muted">
                  {durationShort(p.ageMs)}
                </span>
              </span>
            </li>
          ))}
        </ul>
      )}

      {view.truncated && (
        <p
          data-testid="pending-pods-truncated"
          class="mt-2 text-[11px] leading-snug text-text-muted"
        >
          Counted across the first {view.counted} of {view.total} pods.
        </p>
      )}

      <a
        href="/workloads/pods"
        class="mt-3 block text-xs text-accent no-underline"
      >
        View all pods →
      </a>
    </WidgetShell>
  );
}

registerWidget({
  id: "pending-pods",
  title: "Pending Pods",
  family: "workloads",
  scopes: ["overview"],
  sources: ["pods-list"],
  // No `familyStatus`: pods are core, present on every cluster, and a
  // refusal to list them is a permission fact the source state already
  // carries.
  //
  // Pinned on five sides -- here, two literals in registry_test.ts, the Go
  // catalog and two literals in parity_test.go (KTD7). Chosen once.
  minW: 3,
  minH: 3,
  defaultW: 4,
  defaultH: 5,
  modes: ["normal"],
  render: () => <PendingPods />,
});
