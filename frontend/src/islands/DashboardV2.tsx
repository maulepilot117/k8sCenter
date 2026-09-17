import { useSignal } from "@preact/signals";
import type { JSX } from "preact";
import { useEffect } from "preact/hooks";
import DashboardGrid from "@/components/dashboard/DashboardGrid.tsx";
import { Skeleton } from "@/components/ui/Skeleton.tsx";
// Registers every shipped widget before first render.
import "@/components/dashboard/widgets/index.ts";
import { dashboardData } from "@/lib/dashboard/data.ts";
import { DEFAULT_OVERVIEW_LAYOUT } from "@/lib/dashboard/default-layout.ts";
import { getWidget } from "@/lib/dashboard/registry.ts";
import type { DataSourceKey } from "@/lib/dashboard/types.ts";
import type {
  ClusterInfoData,
  DashboardSummary,
} from "@/lib/dashboard/wire-types.ts";
import { IS_BROWSER } from "@/src/lib/is-browser.ts";

const TIME_RANGES = ["15m", "1h", "6h", "24h"] as const;
type TimeRange = (typeof TIME_RANGES)[number];

// Shared by the SSR placeholder and the hydrated root. Preact hydration keeps
// the server-rendered root's props, so the two must not diverge.
const ROOT_STYLE: JSX.CSSProperties = { minHeight: "400px" };

// Every source the default layout's widgets read, plus the two the header
// reads for its subtitle.
const SOURCES: DataSourceKey[] = [
  ...new Set<DataSourceKey>([
    "cluster-info",
    "dashboard-summary",
    ...DEFAULT_OVERVIEW_LAYOUT.items.flatMap(
      (i) => getWidget(i.id)?.sources ?? [],
    ),
  ]),
];

export default function DashboardV2() {
  const timeRange = useSignal<TimeRange>("1h");
  // Edit mode is deliberately not persisted, and neither is the layout it
  // produces: P3 adds storage. Until then a reload is the way back.
  const editing = useSignal(false);

  useEffect(() => {
    if (!IS_BROWSER) return;
    dashboardData.ensure(SOURCES, timeRange.value);
  }, [timeRange.value]);

  useEffect(() => {
    if (!IS_BROWSER) return;
    const stop = dashboardData.startRefresh();
    return () => {
      stop();
      dashboardData.abort();
    };
  }, []);

  if (!IS_BROWSER) {
    return <div style={ROOT_STYLE} />;
  }

  const info = dashboardData.state<ClusterInfoData>("cluster-info");
  const summary = dashboardData.state<DashboardSummary>("dashboard-summary");
  // Wait for both to settle, so the subtitle never shows "0 nodes" for the
  // moment between one response and the other.
  const headerReady = [info, summary].every(
    (st) => st.data !== null || st.error !== null,
  );
  const nodeCount = summary.data?.nodes.total ?? info.data?.nodeCount ?? 0;
  const podCount = summary.data?.pods.total ?? 0;
  const clusterName = info.data?.platform ?? info.data?.clusterID ?? "cluster";

  return (
    <div style={ROOT_STYLE}>
      <div
        style={{
          display: "flex",
          alignItems: "flex-start",
          justifyContent: "space-between",
          marginBottom: "20px",
          gap: "16px",
          flexWrap: "wrap",
        }}
      >
        <div>
          <h1
            style={{
              margin: 0,
              fontSize: "24px",
              fontWeight: 700,
              letterSpacing: "-0.02em",
              color: "var(--text-primary)",
            }}
          >
            Cluster Overview
          </h1>
          {headerReady ? (
            <div
              style={{
                fontSize: "13px",
                color: "var(--text-muted)",
                marginTop: "4px",
              }}
            >
              {[
                clusterName,
                `${nodeCount} node${nodeCount !== 1 ? "s" : ""}`,
                `${podCount} pods`,
              ].join(" · ")}
            </div>
          ) : (
            <Skeleton class="h-4 w-80 mt-1" />
          )}
        </div>

        <div style={{ display: "flex", alignItems: "center", gap: "8px" }}>
          <button
            type="button"
            data-testid="edit-layout"
            aria-pressed={editing.value}
            onClick={() => {
              editing.value = !editing.value;
            }}
            style={{
              padding: "7px 14px",
              borderRadius: "8px",
              border: "1px solid var(--glass-border)",
              cursor: "pointer",
              fontSize: "12px",
              fontWeight: 500,
              background: editing.value
                ? "var(--accent)"
                : "var(--glass-surface)",
              color: editing.value ? "var(--bg-base)" : "var(--text-muted)",
              transition: "background 0.15s, color 0.15s",
            }}
          >
            {editing.value ? "Done" : "Edit layout"}
          </button>

          <div
            style={{
              display: "flex",
              gap: "2px",
              background: "var(--glass-surface)",
              border: "1px solid var(--glass-border)",
              borderRadius: "8px",
              padding: "3px",
            }}
          >
            {TIME_RANGES.map((r) => (
              <button
                key={r}
                type="button"
                onClick={() => {
                  timeRange.value = r;
                }}
                style={{
                  padding: "5px 12px",
                  borderRadius: "6px",
                  border: "none",
                  cursor: "pointer",
                  fontSize: "12px",
                  fontWeight: 500,
                  background:
                    timeRange.value === r ? "var(--accent)" : "transparent",
                  color:
                    timeRange.value === r
                      ? "var(--bg-base)"
                      : "var(--text-muted)",
                  transition: "background 0.15s, color 0.15s",
                }}
              >
                {r}
              </button>
            ))}
          </div>
        </div>
      </div>

      <DashboardGrid
        initial={DEFAULT_OVERVIEW_LAYOUT}
        editable={editing.value}
      />
    </div>
  );
}
