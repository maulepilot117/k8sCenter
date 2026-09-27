import BarRow from "@/components/charts/BarRow.tsx";
import WidgetShell from "@/components/ui/WidgetShell.tsx";
import type { CoveredSummary } from "@/lib/dashboard/coverage.ts";
import { withheldReason } from "@/lib/dashboard/coverage.ts";
import { dashboardData } from "@/lib/dashboard/data.ts";
import { registerWidget } from "@/lib/dashboard/registry.ts";

/**
 * Node capacity: CPU, memory, pod and readiness bars, with a readiness
 * summary in the shell's action slot.
 *
 * Lifted from the pre-registry DashboardV2 card. As with cluster-health, the
 * island's `?? info?.nodeCount` fallback is dropped: WidgetHost guarantees the
 * summary is non-null before render, and `0 ?? x` is `0`, so the cluster-info
 * operand was unreachable and declaring that source would only gate this card
 * on an endpoint it never reads.
 */
function Nodes() {
  const s = dashboardData.state<CoveredSummary>("dashboard-summary").data;

  const nodeCount = s?.nodes.total ?? 0;
  const nodesReady = s?.nodes.ready ?? 0;
  const podCount = s?.pods.total ?? 0;
  const cpuPct = Math.round(s?.cpu?.percentage ?? 0);
  const memPct = Math.round(s?.memory?.percentage ?? 0);

  // The host gates this card on `nodes` only; CPU, memory and pods are other
  // sections, and on a remote cluster the first two are never observed. Their
  // bars go empty with an em-dash and the reason as a tooltip rather than
  // showing the backend's placeholder 0%.
  const cpuWithheld = withheldReason(s, "cpu") ?? undefined;
  const memWithheld = withheldReason(s, "memory") ?? undefined;
  const podsWithheld = withheldReason(s, "pods") ?? undefined;

  return (
    <WidgetShell
      title="Nodes"
      action={
        nodeCount > 0 ? (
          <span
            style={{
              fontSize: "12px",
              color:
                nodesReady === nodeCount ? "var(--success)" : "var(--warning)",
            }}
          >
            {nodesReady} ready
            {nodeCount > nodesReady
              ? ` · ${nodeCount - nodesReady} under pressure`
              : ""}
          </span>
        ) : undefined
      }
    >
      {nodeCount === 0 ? (
        <div
          style={{
            color: "var(--text-muted)",
            fontSize: "13px",
            textAlign: "center",
            padding: "24px 0",
          }}
        >
          No node data available
        </div>
      ) : (
        <div>
          <div title={cpuWithheld}>
            <BarRow
              label="CPU"
              value={cpuWithheld ? 0 : cpuPct}
              max={100}
              suffix={cpuWithheld ? "—" : `${cpuPct}%`}
              color="var(--accent)"
            />
          </div>
          <div title={memWithheld}>
            <BarRow
              label="Memory"
              value={memWithheld ? 0 : memPct}
              max={100}
              suffix={memWithheld ? "—" : `${memPct}%`}
              color="var(--accent-secondary)"
            />
          </div>
          <div title={podsWithheld}>
            <BarRow
              label="Pods"
              value={podsWithheld ? 0 : podCount}
              max={Math.max(podCount, 440)}
              suffix={podsWithheld ? "—" : String(podCount)}
              color="var(--success)"
            />
          </div>
          {/* Node readiness bar */}
          <BarRow
            label="Ready"
            value={nodesReady}
            max={nodeCount || 1}
            suffix={`${nodesReady}/${nodeCount}`}
            color={
              nodesReady === nodeCount ? "var(--success)" : "var(--warning)"
            }
          />
        </div>
      )}
      <a
        href="/cluster/nodes"
        style={{
          display: "block",
          marginTop: "12px",
          fontSize: "12px",
          color: "var(--accent)",
          textDecoration: "none",
        }}
      >
        View all nodes →
      </a>
    </WidgetShell>
  );
}

registerWidget({
  id: "nodes",
  title: "Nodes",
  family: "cluster",
  scopes: ["overview"],
  sources: ["dashboard-summary"],
  minW: 3,
  minH: 4,
  defaultW: 4,
  defaultH: 6,
  // Normal only: this card has one rendering. `modes` declares what a
  // widget actually implements, and a compact variant would be new UI that
  // D5's verbatim-extraction rule forbids. pickMode falls back toward
  // normal from both directions, so a small box still renders correctly.
  modes: ["normal"],
  render: () => <Nodes />,
});
