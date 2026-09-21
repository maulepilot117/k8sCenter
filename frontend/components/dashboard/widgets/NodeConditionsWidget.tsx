import WidgetShell from "@/components/ui/WidgetShell.tsx";
import { dashboardData } from "@/lib/dashboard/data.ts";
// The four-condition selection, the silent/not-ready distinction and the
// ranking live in lib/, under test, because none of them is a field read
// (D-10, KTD8). Do not inline them.
import type { NodeConditionKind, NodeIssue } from "@/lib/dashboard/pressure.ts";
import {
  NODE_CONDITION_LABEL,
  NODE_PAGE_HREF,
  nodeConditionsView,
  nodeHref,
} from "@/lib/dashboard/pressure.ts";
import { registerWidget } from "@/lib/dashboard/registry.ts";
import type { ResourceListPage } from "@/lib/dashboard/wire-types.ts";

/** How many nodes the card lists. The header counts cover the whole page. */
const ROW_LIMIT = 6;

const CONDITION_COLOR: Readonly<Record<NodeConditionKind, string>> = {
  "not-ready": "var(--error)",
  "memory-pressure": "var(--warning)",
  "disk-pressure": "var(--warning)",
  "pid-pressure": "var(--warning)",
};

/**
 * The nodes that need attention, and only those.
 *
 * A Node object carries a dozen conditions and a cluster with a CNI, a CSI
 * driver and a node problem detector carries more. This card shows four of
 * them -- NotReady and the memory, disk and PID pressures -- because those are
 * the ones that mean the node cannot be relied on to keep running what is on
 * it: the kubelet acts on a pressure condition by evicting pods, and a
 * NotReady node is already taking none. The full condition list is on the node
 * page, which is where the operator goes next.
 *
 * A healthy node is deliberately absent from the list rather than listed in
 * green. Listing every node turns a findings card into an inventory that
 * happens to be sorted, and the shell's height is finite -- the nodes worth
 * seeing would be pushed off it by the ones that are fine. The header says how
 * many are clear.
 */
function NodeConditions() {
  const page = dashboardData.state<ResourceListPage>("nodes-list").data;
  const view = nodeConditionsView(page, ROW_LIMIT);

  return (
    <WidgetShell
      title="Node Conditions"
      action={
        view.affected > 0 ? (
          <span
            data-testid="node-conditions-summary"
            class="text-xs"
            style={{ color: "var(--error)" }}
          >
            {view.affected} of {view.counted} affected
          </span>
        ) : view.clear > 0 ? (
          <span
            data-testid="node-conditions-summary"
            class="text-xs"
            style={{ color: "var(--success)" }}
          >
            {view.clear} clear
          </span>
        ) : undefined
      }
    >
      {!view.readable ? (
        // The nodes route answered with something this build cannot read as a
        // list. Every count beside this is zero because there was nothing to
        // count, so the empty state below would render a cluster where every
        // node is ready -- the most reassuring thing this card can say, over
        // a question that was never answered.
        <p
          data-testid="node-conditions-unreadable-page"
          class="py-4 text-center text-xs text-text-muted"
        >
          The node roll-up returned a result this card cannot read.
        </p>
      ) : view.nodes.length === 0 ? (
        <p
          data-testid="node-conditions-empty"
          class="py-4 text-center text-xs text-text-muted"
        >
          {view.clear === 0
            ? "No node data available."
            : `All ${view.clear} node${view.clear === 1 ? "" : "s"} are ready and reporting no pressure.`}
        </p>
      ) : (
        <ul class="flex flex-col gap-1.5" data-testid="node-conditions-list">
          {view.nodes.map((node) => (
            <NodeRow key={node.name} node={node} />
          ))}
        </ul>
      )}

      {view.unreadable > 0 && (
        <p
          data-testid="node-conditions-unreadable"
          class="mt-2 text-[11px] leading-snug text-text-muted"
        >
          {view.unreadable} entr{view.unreadable === 1 ? "y" : "ies"} could not
          be read as a node.
        </p>
      )}

      {view.truncated && (
        <p
          data-testid="node-conditions-truncated"
          class="mt-2 text-[11px] leading-snug text-text-muted"
        >
          Checked the first {view.counted} of {view.total} nodes.
        </p>
      )}

      <a
        href={NODE_PAGE_HREF}
        class="mt-3 block text-xs text-accent no-underline"
      >
        View all nodes →
      </a>
    </WidgetShell>
  );
}

/**
 * One node's row.
 *
 * A silent node is labelled as such rather than simply NotReady. They lead to
 * different places: a node that reported itself NotReady is answering, and the
 * reason is on the node; a node whose Ready condition is `Unknown` stopped
 * answering, and the control plane is showing the last thing it heard.
 */
function NodeRow({ node }: { node: NodeIssue }) {
  const lead = node.conditions[0];

  return (
    <li class="flex items-center justify-between gap-2 text-xs">
      <span class="flex min-w-0 items-center gap-2">
        <span
          aria-hidden="true"
          class="size-2 shrink-0 rounded-full"
          style={{ background: CONDITION_COLOR[lead] }}
        />
        <a
          href={nodeHref(node.name)}
          title={node.name}
          class="truncate text-text-secondary no-underline hover:text-accent"
        >
          {node.name}
        </a>
        {node.silent && (
          <span
            class="shrink-0 rounded-md px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-wide"
            style={{ background: "var(--error-dim)", color: "var(--error)" }}
            title="the kubelet has stopped reporting; this is the last status the control plane heard"
          >
            not reporting
          </span>
        )}
      </span>
      <span class="flex shrink-0 items-center gap-1">
        {node.conditions.map((kind) => (
          <span
            key={kind}
            class="rounded-md px-1.5 py-0.5 text-[10px] font-semibold"
            style={{
              background: "var(--bg-hover)",
              color: CONDITION_COLOR[kind],
            }}
          >
            {NODE_CONDITION_LABEL[kind]}
          </span>
        ))}
      </span>
    </li>
  );
}

registerWidget({
  id: "node-conditions",
  title: "Node Conditions",
  family: "reliability",
  scopes: ["overview"],
  sources: ["nodes-list"],
  // No `familyStatus`. Node is core `v1` and every cluster has some; there is
  // no discovery route to ask and nothing to be absent.
  //
  // Wider and taller than the smallest cards: a row carries a node name plus
  // up to four condition chips, and a node name is routinely something like
  // `ip-10-0-42-118.eu-west-1.compute.internal`.
  //
  // Pinned on five sides -- here, two literals in registry_test.ts, the Go
  // catalog and two literals in parity_test.go (KTD7). Chosen once.
  minW: 4,
  minH: 3,
  defaultW: 6,
  defaultH: 5,
  modes: ["normal"],
  render: () => <NodeConditions />,
});
