import { useSignal } from "@preact/signals";
import type { JSX } from "preact";
import { useEffect } from "preact/hooks";
import WidgetHost from "@/components/dashboard/WidgetHost.tsx";
import { Skeleton } from "@/components/ui/Skeleton.tsx";
// Registers every shipped widget before first render.
import "@/components/dashboard/widgets/index.ts";
import { dashboardData } from "@/lib/dashboard/data.ts";
import {
  DEFAULT_OVERVIEW_LAYOUT,
  defaultItem,
  type FlexSlot,
  flexSlotIds,
  OVERVIEW_FLEX_ROWS,
} from "@/lib/dashboard/default-layout.ts";
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

const ROW_GAP = "var(--grid-gap, 16px)";
const ROW_STYLE: JSX.CSSProperties = {
  display: "flex",
  flexWrap: "wrap",
  gap: ROW_GAP,
};

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

/**
 * One widget in its pre-registry flex slot.
 *
 * Placement stays the old flex rows so this unit is visually identical to the
 * page it replaces; the twelve-column grid arrives with P2.
 */
function Slot({ slot }: { slot: FlexSlot }) {
  const item = defaultItem(slot.id);
  const def = getWidget(item.id);
  if (!def) return null;

  const host = (
    <WidgetHost
      def={def}
      params={item.params}
      placeholderHeight={slot.placeholder}
    />
  );
  if (!slot.flex) return host;
  return <div style={{ flex: slot.flex, minWidth: slot.minWidth }}>{host}</div>;
}

export default function DashboardV2() {
  const timeRange = useSignal<TimeRange>("1h");

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

      {OVERVIEW_FLEX_ROWS.map((row, i) => (
        <div
          key={flexSlotIds([row])[0]}
          style={{
            ...ROW_STYLE,
            marginBottom: i < OVERVIEW_FLEX_ROWS.length - 1 ? ROW_GAP : 0,
          }}
        >
          {row.map((cell) =>
            "tiles" in cell ? (
              <div
                key={cell.tiles[0].id}
                style={{
                  flex: cell.flex,
                  display: "grid",
                  gridTemplateColumns: "1fr 1fr",
                  gap: ROW_GAP,
                }}
              >
                {cell.tiles.map((t) => (
                  <Slot key={t.id} slot={t} />
                ))}
              </div>
            ) : (
              <Slot key={cell.id} slot={cell} />
            ),
          )}
        </div>
      ))}
    </div>
  );
}
