import WidgetShell from "@/components/ui/WidgetShell.tsx";
import { dashboardData } from "@/lib/dashboard/data.ts";
// The classification and the ranking live in lib/, under test, and are shared
// with pending-pods so the two cards cannot disagree about the same pod
// (D-10, KTD8). Do not inline them.
import { podRestartsView } from "@/lib/dashboard/pod-health.ts";
import { registerWidget } from "@/lib/dashboard/registry.ts";
import type { ResourceListPage } from "@/lib/dashboard/wire-types.ts";

/** How many rows the card lists. The header counts cover the whole page. */
const ROW_LIMIT = 6;

/**
 * Pods ranked by restarts, with anything currently crash-looping first.
 *
 * The ordering is the judgement, and it is not "most restarts". A pod that
 * restarted forty times during an incident last week and has been up since is
 * history; one looping right now is an outage in progress. Sorting purely by
 * count buries the second under the first, so the loop wins regardless of
 * count and the count breaks ties inside each group.
 *
 * Note that a crash-looping pod reports phase `Running` -- the kubelet keeps
 * restarting it -- which is why the state cannot be read off the phase and
 * why `classifyPod` promotes the loop to a state of its own.
 *
 * Pods with no loop and no restarts are left off entirely. The card is a list
 * of things worth looking at, and on a healthy cluster the empty list is the
 * finding; padding it with quiet pods would make it look like a pod inventory
 * that happens to be sorted.
 */
function PodRestarts() {
  const page = dashboardData.state<ResourceListPage>("pods-list").data;
  const view = podRestartsView(page, ROW_LIMIT);

  return (
    <WidgetShell
      title="Pod Restarts"
      action={
        view.crashLooping > 0 ? (
          <span
            data-testid="pod-restarts-summary"
            class="text-xs"
            style={{ color: "var(--error)" }}
          >
            {view.crashLooping} crash-looping
          </span>
        ) : view.restarting > 0 ? (
          <span
            data-testid="pod-restarts-summary"
            class="text-xs text-text-muted"
          >
            {view.totalRestarts} restart
            {view.totalRestarts === 1 ? "" : "s"}
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
          data-testid="pod-restarts-unreadable-page"
          class="py-4 text-center text-xs text-text-muted"
        >
          The pod roll-up returned a result this card cannot read.
        </p>
      ) : view.pods.length === 0 ? (
        <p
          data-testid="pod-restarts-empty"
          class="py-4 text-center text-xs text-text-muted"
        >
          No pod has restarted.
        </p>
      ) : (
        <ul class="flex flex-col gap-1.5" data-testid="pod-restarts-list">
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
                      p.state === "crash-looping"
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
                {p.state === "crash-looping" && (
                  <span
                    class="shrink-0 rounded-md px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-wide"
                    style={{
                      background: "var(--error-dim)",
                      color: "var(--error)",
                    }}
                  >
                    looping
                  </span>
                )}
              </span>
              <span
                class="shrink-0 font-mono font-semibold"
                style={{
                  color:
                    p.state === "crash-looping"
                      ? "var(--error)"
                      : "var(--text-primary)",
                }}
              >
                {p.restarts}
              </span>
            </li>
          ))}
        </ul>
      )}

      {view.truncated && (
        <p
          data-testid="pod-restarts-truncated"
          class="mt-2 text-[11px] leading-snug text-text-muted"
        >
          Ranked across the first {view.counted} of {view.total} pods.
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
  id: "pod-restarts",
  title: "Pod Restarts",
  family: "workloads",
  scopes: ["overview"],
  // The same source pending-pods reads, which is the point of sharing the
  // classification: the cache fetches `pods-list` once and both cards select
  // over it.
  sources: ["pods-list"],
  // Pinned on five sides -- here, two literals in registry_test.ts, the Go
  // catalog and two literals in parity_test.go (KTD7). Chosen once.
  minW: 3,
  minH: 3,
  defaultW: 4,
  defaultH: 5,
  modes: ["normal"],
  render: () => <PodRestarts />,
});
