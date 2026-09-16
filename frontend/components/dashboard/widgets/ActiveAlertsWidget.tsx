import WidgetShell from "@/components/ui/WidgetShell.tsx";
import { dashboardData } from "@/lib/dashboard/data.ts";
import { registerWidget } from "@/lib/dashboard/registry.ts";
import type { DashboardSummary } from "@/lib/dashboard/wire-types.ts";

/**
 * Firing alert counts: a critical line, a warning remainder, and a link out.
 *
 * Lifted from the pre-registry DashboardV2 card.
 */
function ActiveAlerts() {
  const s = dashboardData.state<DashboardSummary>("dashboard-summary").data;

  const activeAlerts = s?.alerts.active ?? 0;
  const criticalAlerts = s?.alerts.critical ?? 0;
  const warningAlerts = activeAlerts - criticalAlerts;

  return (
    <WidgetShell
      title="Active Alerts"
      action={
        activeAlerts > 0 ? (
          <span
            style={{
              fontSize: "11px",
              fontWeight: 600,
              padding: "3px 8px",
              borderRadius: "10px",
              background:
                criticalAlerts > 0 ? "var(--error-dim)" : "var(--warning-dim)",
              color: criticalAlerts > 0 ? "var(--error)" : "var(--warning)",
            }}
          >
            {activeAlerts} firing
          </span>
        ) : undefined
      }
    >
      {activeAlerts === 0 ? (
        <div
          style={{
            display: "flex",
            flexDirection: "column",
            alignItems: "center",
            gap: "8px",
            padding: "24px 0",
            color: "var(--text-muted)",
            fontSize: "12px",
            textAlign: "center",
          }}
        >
          <svg
            width="20"
            height="20"
            viewBox="0 0 24 24"
            fill="none"
            stroke="var(--success)"
            stroke-width="2"
          >
            <path d="M22 11.08V12a10 10 0 1 1-5.93-9.14" />
            <polyline points="22 4 12 14.01 9 11.01" />
          </svg>
          <span>All clear</span>
        </div>
      ) : (
        <div style={{ display: "flex", flexDirection: "column", gap: "2px" }}>
          {/* Summary line */}
          {criticalAlerts > 0 && (
            <div
              style={{
                display: "flex",
                alignItems: "center",
                gap: "8px",
                padding: "7px 0",
                borderBottom: "1px solid var(--glass-border)",
                marginBottom: "6px",
              }}
            >
              <span
                style={{
                  width: "7px",
                  height: "7px",
                  borderRadius: "50%",
                  background: "var(--error)",
                  flexShrink: 0,
                }}
              />
              <span
                style={{
                  fontSize: "13px",
                  color: "var(--error)",
                  fontWeight: 600,
                }}
              >
                {criticalAlerts} critical
              </span>
            </div>
          )}
          <div
            style={{
              fontSize: "12px",
              color: "var(--text-muted)",
              marginTop: "8px",
            }}
          >
            {warningAlerts > 0 && (
              <span>
                +{warningAlerts} warning{warningAlerts !== 1 ? "s" : ""}
              </span>
            )}
          </div>
          <a
            href="/alerting"
            style={{
              display: "block",
              marginTop: "12px",
              fontSize: "12px",
              color: "var(--accent)",
              textDecoration: "none",
            }}
          >
            View all alerts →
          </a>
        </div>
      )}
    </WidgetShell>
  );
}

registerWidget({
  id: "active-alerts",
  title: "Active Alerts",
  family: "reliability",
  scopes: ["overview"],
  sources: ["dashboard-summary"],
  minW: 2,
  minH: 3,
  defaultW: 3,
  defaultH: 6,
  // Normal only, for the same reason as recent-events: this card has one
  // rendering in the island and D5 extracts it verbatim.
  modes: ["normal"],
  render: () => <ActiveAlerts />,
});
