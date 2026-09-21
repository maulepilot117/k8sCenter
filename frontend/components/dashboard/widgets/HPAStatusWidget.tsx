import WidgetShell from "@/components/ui/WidgetShell.tsx";
// The ceiling rule, the metric pairing and the unknown/at-ceiling/saturated
// distinction all live in lib/, under test, because none of them is a field
// read and this repo has no component test harness (D-10, KTD8). Do not
// inline them.
import type { HPAState, HPAStatus } from "@/lib/dashboard/autoscaling.ts";
import {
  HPA_LIST_HREF,
  hpaHref,
  hpaStatusView,
} from "@/lib/dashboard/autoscaling.ts";
import { dashboardData } from "@/lib/dashboard/data.ts";
import { registerWidget } from "@/lib/dashboard/registry.ts";
import type { ResourceListPage } from "@/lib/dashboard/wire-types.ts";

/** How many rows the card lists. The header counts cover the whole page. */
const ROW_LIMIT = 6;

/** One colour per state, from the theme's custom properties. `steady` is the
 * only green, and it is earned: both the replica count and the metric were
 * read. `unknown` is muted rather than coloured, because a colour would be a
 * claim. */
const STATE_COLOR: Readonly<Record<HPAState, string>> = {
  saturated: "var(--error)",
  "at-ceiling": "var(--warning)",
  scaling: "var(--accent)",
  steady: "var(--success)",
  unknown: "var(--text-muted)",
};

const STATE_BADGE: Readonly<Record<HPAState, string>> = {
  saturated: "out of room",
  "at-ceiling": "at max",
  scaling: "scaling",
  steady: "",
  unknown: "unreadable",
};

/**
 * HorizontalPodAutoscalers, ranked by how close each one is to being out of
 * room.
 *
 * The finding this card exists for is not the replica numbers. `10/10` is
 * what a healthy autoscaler sized to its ceiling prints and what one whose
 * `maxReplicas` is too low prints; the metric is the only thing that tells
 * them apart, so the card reads both and flags only the second. An autoscaler
 * at its maximum that is comfortably serving its target is normal, and a card
 * that warned about it would be ignored within a week.
 *
 * One source, and not the counts route beside it as the workload roll-up has:
 * everything on this card is about HPAs, so a caller who cannot list them has
 * nothing to be shown and the shell's permission state is the whole answer.
 * The workload roll-up needs counts because it covers three kinds and must
 * still render the two a caller CAN see.
 */
function Autoscalers() {
  const page = dashboardData.state<ResourceListPage>("hpas-list").data;
  const view = hpaStatusView(page, ROW_LIMIT);

  return (
    <WidgetShell
      title="Autoscalers"
      action={
        view.saturated > 0 ? (
          <span
            data-testid="hpa-status-summary"
            class="text-xs"
            style={{ color: "var(--error)" }}
          >
            {view.saturated} out of room
          </span>
        ) : view.atCeiling > 0 ? (
          <span
            data-testid="hpa-status-summary"
            class="text-xs text-text-muted"
          >
            {view.atCeiling} at max
          </span>
        ) : undefined
      }
    >
      {view.hpas.length === 0 ? (
        <p
          data-testid="hpa-status-empty"
          class="py-4 text-center text-xs text-text-muted"
        >
          No HorizontalPodAutoscalers on this cluster.
        </p>
      ) : (
        <ul class="flex flex-col gap-1.5" data-testid="hpa-status-list">
          {view.hpas.map((h) => (
            <HPARow key={`${h.namespace}/${h.name}`} hpa={h} />
          ))}
        </ul>
      )}

      {view.unknown > 0 && (
        <p
          data-testid="hpa-status-unknown"
          class="mt-2 text-[11px] leading-snug text-text-muted"
        >
          {view.unknown} autoscaler{view.unknown === 1 ? "" : "s"} could not be
          read, so {view.unknown === 1 ? "it is" : "they are"} left out of the
          counts above.
        </p>
      )}

      {view.truncated && (
        <p
          data-testid="hpa-status-truncated"
          class="mt-2 text-[11px] leading-snug text-text-muted"
        >
          Ranked across the first {view.counted} of {view.total} autoscalers.
        </p>
      )}

      <a
        href={HPA_LIST_HREF}
        class="mt-3 block text-xs text-accent no-underline"
      >
        View all autoscalers →
      </a>
    </WidgetShell>
  );
}

/**
 * One autoscaler's row.
 *
 * The utilization column renders an em-dash when no metric pair was readable,
 * never a zero: `0%` on this card reads as an idle workload with all the
 * headroom in the world, which is the opposite of what an unreported metric
 * means.
 */
function HPARow({ hpa }: { hpa: HPAStatus }) {
  const badge = STATE_BADGE[hpa.state];
  const replicas =
    hpa.current === null || hpa.max === null
      ? "—"
      : hpa.desired !== null && hpa.desired !== hpa.current
        ? `${hpa.current}→${hpa.desired}/${hpa.max}`
        : `${hpa.current}/${hpa.max}`;

  return (
    <li class="flex items-center justify-between gap-2 text-xs">
      <span class="flex min-w-0 items-center gap-2">
        <span
          aria-hidden="true"
          class="size-2 shrink-0 rounded-full"
          style={{ background: STATE_COLOR[hpa.state] }}
        />
        <a
          href={hpaHref(hpa.namespace, hpa.name)}
          title={[
            `${hpa.namespace}/${hpa.name}`,
            hpa.target === "" ? "" : ` → ${hpa.target}`,
            hpa.min === null || hpa.max === null
              ? ""
              : ` (${hpa.min}–${hpa.max} replicas)`,
          ].join("")}
          class="truncate text-text-secondary no-underline hover:text-accent"
        >
          {hpa.name}
        </a>
        {badge !== "" && (
          <span
            class="shrink-0 rounded-md px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-wide"
            style={{
              background:
                hpa.state === "saturated"
                  ? "var(--error-dim)"
                  : "var(--bg-hover)",
              color: STATE_COLOR[hpa.state],
            }}
          >
            {badge}
          </span>
        )}
      </span>
      <span class="flex shrink-0 items-baseline gap-2 font-mono">
        <span
          class="text-[11px]"
          style={{
            color:
              hpa.utilization === null
                ? "var(--text-muted)"
                : "var(--text-secondary)",
          }}
          title={
            hpa.targetUtilization === null
              ? "No utilization metric has been reported"
              : `target ${hpa.targetUtilization}%`
          }
        >
          {hpa.utilization === null
            ? "—"
            : `${Math.round(hpa.utilization)}%/${hpa.targetUtilization}%`}
        </span>
        <span class="font-semibold" style={{ color: STATE_COLOR[hpa.state] }}>
          {replicas}
        </span>
      </span>
    </li>
  );
}

registerWidget({
  id: "hpa-status",
  title: "Autoscalers",
  family: "workloads",
  scopes: ["overview"],
  sources: ["hpas-list"],
  // No `familyStatus`. HorizontalPodAutoscaler is core `autoscaling/v2`, not a
  // CRD, so there is no discovery route to ask and nothing for the "not
  // installed" state to be true of. A cluster with no autoscalers renders the
  // empty copy, which says so in as many words.
  //
  // Pinned on five sides -- here, two literals in registry_test.ts, the Go
  // catalog and two literals in parity_test.go (KTD7). Chosen once.
  minW: 3,
  minH: 3,
  defaultW: 4,
  defaultH: 5,
  modes: ["normal"],
  render: () => <Autoscalers />,
});
