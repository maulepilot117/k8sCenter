import Donut from "@/components/charts/Donut.tsx";
import WidgetShell from "@/components/ui/WidgetShell.tsx";
import { dashboardData } from "@/lib/dashboard/data.ts";
import { podStatusSegments } from "@/lib/dashboard/pod-status.ts";
import { registerWidget } from "@/lib/dashboard/registry.ts";
import type { DashboardSummary } from "@/lib/dashboard/wire-types.ts";

/**
 * Pod status: a phase donut with the total in its hole, beside a three-row
 * legend.
 *
 * Lifted from the pre-registry DashboardV2 card, including the guard fix that
 * D0 made: the donut is only drawn from real counts when at least one of the
 * three plotted phases is non-zero.
 */
function PodStatus() {
  const s = dashboardData.state<DashboardSummary>("dashboard-summary").data;

  const podCount = s?.pods.total ?? 0;
  const podRunning = s?.pods.running ?? 0;
  const podPending = s?.pods.pending ?? 0;
  const podFailed = s?.pods.failed ?? 0;

  // Segment derivation lives in lib/dashboard/pod-status.ts so the guard it
  // carries is unit-tested. It has already regressed into a user-visible bug
  // once, and this repo has no component test harness.
  const donutSegments = podStatusSegments(podRunning, podPending, podFailed);

  return (
    <WidgetShell title="Pod Status">
      <div
        style={{
          display: "flex",
          alignItems: "center",
          gap: "20px",
          flexWrap: "wrap",
        }}
      >
        <Donut
          segments={donutSegments}
          size={112}
          thickness={18}
          center={
            <div
              style={{
                display: "flex",
                flexDirection: "column",
                alignItems: "center",
              }}
            >
              <span
                style={{
                  fontSize: "22px",
                  fontWeight: 750,
                  color: "var(--text-primary)",
                  lineHeight: 1,
                }}
              >
                {podCount}
              </span>
              <span
                style={{
                  fontSize: "10px",
                  color: "var(--text-muted)",
                  marginTop: "2px",
                }}
              >
                pods
              </span>
            </div>
          }
        />
        {/* Legend */}
        <div
          style={{
            display: "flex",
            flexDirection: "column",
            gap: "8px",
            flex: 1,
          }}
        >
          {[
            { label: "Running", value: podRunning, color: "var(--success)" },
            { label: "Pending", value: podPending, color: "var(--warning)" },
            { label: "Failed", value: podFailed, color: "var(--error)" },
          ].map(({ label, value, color }) => (
            <div
              key={label}
              style={{
                display: "flex",
                alignItems: "center",
                justifyContent: "space-between",
                gap: "8px",
              }}
            >
              <div
                style={{ display: "flex", alignItems: "center", gap: "6px" }}
              >
                <span
                  style={{
                    width: "8px",
                    height: "8px",
                    borderRadius: "50%",
                    background: color,
                    flexShrink: 0,
                  }}
                />
                <span
                  style={{ fontSize: "12px", color: "var(--text-secondary)" }}
                >
                  {label}
                </span>
              </div>
              <span
                style={{
                  fontSize: "13px",
                  fontWeight: 600,
                  color: "var(--text-primary)",
                  fontFamily: "var(--font-mono, monospace)",
                }}
              >
                {value}
              </span>
            </div>
          ))}
        </div>
      </div>
    </WidgetShell>
  );
}

registerWidget({
  id: "pod-status",
  title: "Pod Status",
  family: "workloads",
  scopes: ["overview"],
  sources: ["dashboard-summary"],
  minW: 3,
  minH: 4,
  defaultW: 5,
  defaultH: 6,
  // Normal only -- one rendering in the island; see NodesWidget.
  modes: ["normal"],
  render: () => <PodStatus />,
});
