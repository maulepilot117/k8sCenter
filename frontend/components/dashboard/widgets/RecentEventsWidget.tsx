import WidgetShell from "@/components/ui/WidgetShell.tsx";
import { dashboardData } from "@/lib/dashboard/data.ts";
import { registerWidget } from "@/lib/dashboard/registry.ts";
import { age } from "@/lib/format.ts";
import type { K8sEvent } from "@/lib/k8s-types.ts";

/**
 * Short forms for the noisiest resource kinds, so an event row reads
 * `deploy/api` rather than `deployment/api`.
 *
 * Module scope, not inside the row loop: the island rebuilt this object once
 * per event on every render.
 */
const KIND_ABBR: Record<string, string> = {
  deployment: "deploy",
  service: "svc",
  replicaset: "rs",
  statefulset: "sts",
  daemonset: "ds",
  persistentvolumeclaim: "pvc",
  horizontalpodautoscaler: "hpa",
  configmap: "cm",
  serviceaccount: "sa",
  networkpolicy: "netpol",
};

/**
 * The ten most recent cluster events, newest first.
 *
 * Lifted from the pre-registry DashboardV2 card.
 */
function RecentEvents() {
  const raw = dashboardData.state<K8sEvent[]>("recent-events").data;
  // The island guarded with Array.isArray before assigning, because an
  // endpoint returning no body leaves `data` undefined rather than null and
  // the host's gate only rejects null. Same guard, same reason.
  const events = Array.isArray(raw) ? raw : [];

  return (
    <WidgetShell
      title="Recent Events"
      action={
        events.length > 0 ? (
          <a
            href="/cluster/events"
            style={{
              fontSize: "12px",
              color: "var(--accent)",
              textDecoration: "none",
            }}
          >
            View all →
          </a>
        ) : undefined
      }
    >
      {events.length === 0 ? (
        <div
          style={{
            color: "var(--text-muted)",
            fontSize: "12px",
            textAlign: "center",
            padding: "24px 0",
            display: "flex",
            flexDirection: "column",
            alignItems: "center",
            gap: "8px",
          }}
        >
          <svg
            width="24"
            height="24"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            stroke-width="1.5"
            style={{ opacity: 0.4 }}
          >
            <circle cx="12" cy="12" r="10" />
            <path d="M8 14s1.5 2 4 2 4-2 4-2" />
            <line x1="9" y1="9" x2="9.01" y2="9" />
            <line x1="15" y1="9" x2="15.01" y2="9" />
          </svg>
          <span>All quiet — no recent events</span>
        </div>
      ) : (
        <div style={{ display: "flex", flexDirection: "column", gap: "2px" }}>
          {events.map((evt, idx) => {
            const isWarning = evt.type === "Warning";
            const kind = evt.involvedObject?.kind?.toLowerCase() ?? "";
            const prefix = KIND_ABBR[kind] ?? kind;
            const resourceLabel = evt.involvedObject?.name
              ? `${prefix}/${evt.involvedObject.name}`
              : "";

            return (
              <div
                key={`${evt.metadata?.uid ?? idx}`}
                style={{
                  display: "flex",
                  alignItems: "flex-start",
                  gap: "10px",
                  padding: "7px 8px",
                  borderRadius: "var(--radius-sm)",
                }}
              >
                <span
                  style={{
                    width: "7px",
                    height: "7px",
                    borderRadius: "50%",
                    marginTop: "4px",
                    flexShrink: 0,
                    background: isWarning ? "var(--warning)" : "var(--accent)",
                  }}
                />
                <div style={{ flex: 1, minWidth: 0 }}>
                  <div
                    style={{
                      fontSize: "12px",
                      color: "var(--text-secondary)",
                      lineHeight: 1.4,
                      overflow: "hidden",
                      textOverflow: "ellipsis",
                      whiteSpace: "nowrap",
                    }}
                  >
                    {resourceLabel && (
                      <span
                        style={{
                          color: "var(--accent)",
                          fontFamily: "var(--font-mono, monospace)",
                          fontSize: "11px",
                        }}
                      >
                        {resourceLabel}
                      </span>
                    )}
                    {resourceLabel ? " " : ""}
                    {evt.message}
                  </div>
                  <div
                    style={{
                      fontSize: "10px",
                      color: "var(--text-muted)",
                      marginTop: "2px",
                      fontFamily: "var(--font-mono, monospace)",
                    }}
                  >
                    {[evt.source?.component, evt.involvedObject?.namespace]
                      .filter(Boolean)
                      .join(" · ")}
                  </div>
                </div>
                <span
                  style={{
                    fontSize: "11px",
                    color: "var(--text-muted)",
                    fontFamily: "var(--font-mono, monospace)",
                    whiteSpace: "nowrap",
                    marginTop: "1px",
                  }}
                >
                  {evt.metadata?.creationTimestamp
                    ? age(evt.metadata.creationTimestamp)
                    : ""}
                </span>
              </div>
            );
          })}
        </div>
      )}
    </WidgetShell>
  );
}

registerWidget({
  id: "recent-events",
  title: "Recent Events",
  family: "reliability",
  scopes: ["overview"],
  sources: ["recent-events"],
  minW: 3,
  minH: 3,
  defaultW: 5,
  defaultH: 6,
  // Normal only, deliberately under-declaring against the plan. The plan gives
  // this widget all three modes -- compact a count, expanded a message column
  // -- but those are renderings that do not exist in the island, and D5's rule
  // is verbatim extraction: inventing them here is new UI the fidelity exit
  // bar cannot check. Declaring a mode the widget does not implement would
  // also make data-widget-mode report "expanded" over the normal list.
  // pickMode falls back toward normal from both directions, so this renders
  // correctly at every size until someone builds the other two.
  modes: ["normal"],
  render: () => <RecentEvents />,
});
