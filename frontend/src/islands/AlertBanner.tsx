import { useCallback, useEffect, useState } from "react";
import { apiGet } from "@/lib/api.ts";
import type { AlertEvent } from "@/lib/k8s-types.ts";
import { useIsClient } from "@/src/lib/use-is-client.ts";
import {
  EVENT_ADDED,
  EVENT_DELETED,
  EVENT_RESYNC,
  subscribe,
} from "@/src/lib/ws.ts";

export default function AlertBanner() {
  const isClient = useIsClient();
  const [alerts, setAlerts] = useState<AlertEvent[]>([]);
  const [dismissed, setDismissed] = useState(false);

  const fetchAlerts = useCallback(() => {
    apiGet<AlertEvent[]>("/v1/alerts")
      .then((res) => {
        setAlerts(res.data ?? []);
      })
      .catch(() => {
        // Silently fail — banner is non-critical
      });
  }, []);

  useEffect(() => {
    // Fetch initial state via REST
    fetchAlerts();

    // Subscribe to real-time alert events via WebSocket.
    //"alerts" is in alwaysAllowKinds — JWT auth only, no RBAC check.
    const unsubscribe = subscribe(
      "alertbanner",
      "alerts",
      "", // all namespaces
      (eventType, object) => {
        if (eventType === EVENT_RESYNC) {
          fetchAlerts();
          return;
        }

        const alert = object as AlertEvent;
        if (!alert?.fingerprint) return;

        if (eventType === EVENT_ADDED) {
          // Add if not already present (deduplicate by fingerprint)
          setAlerts((prev) =>
            prev.some((a) => a.fingerprint === alert.fingerprint)
              ? prev
              : [...prev, alert],
          );
        } else if (eventType === EVENT_DELETED) {
          // Remove resolved alert
          setAlerts((prev) =>
            prev.filter((a) => a.fingerprint !== alert.fingerprint),
          );
        }
      },
    );

    return unsubscribe;
  }, [fetchAlerts]);

  if (!isClient || dismissed || alerts.length === 0) {
    return null;
  }

  const critical = alerts.filter((a) => a.severity === "critical").length;
  const warning = alerts.filter((a) => a.severity === "warning").length;
  const total = alerts.length;

  return (
    <div className="bg-danger-dim border-b border-danger px-4 py-2">
      <div className="flex items-center justify-between max-w-7xl mx-auto">
        <a
          href="/alerting"
          className="flex items-center gap-2 text-sm text-danger hover:text-danger"
        >
          <svg
            className="w-4 h-4"
            fill="none"
            stroke="currentColor"
            viewBox="0 0 24 24"
          >
            <path
              strokeLinecap="round"
              strokeLinejoin="round"
              strokeWidth="2"
              d="M15 17h5l-1.405-1.405A2.032 2.032 0 0118 14.158V11a6.002 6.002 0 00-4-5.659V5a2 2 0 10-4 0v.341C7.67 6.165 6 8.388 6 11v3.159c0 .538-.214 1.055-.595 1.436L4 17h5m6 0v1a3 3 0 11-6 0v-1m6 0H9"
            />
          </svg>
          <span className="font-medium">
            {total} active alert{total !== 1 ? "s" : ""}
          </span>
          {critical > 0 && (
            <span className="bg-danger-dim text-danger text-xs px-1.5 py-0.5 rounded-full">
              {critical} critical
            </span>
          )}
          {warning > 0 && (
            <span className="bg-warning-dim text-warning text-xs px-1.5 py-0.5 rounded-full">
              {warning} warning
            </span>
          )}
        </a>
        <button
          type="button"
          onClick={() => setDismissed(true)}
          className="text-danger hover:text-danger"
          aria-label="Dismiss"
        >
          <svg className="w-4 h-4" fill="currentColor" viewBox="0 0 20 20">
            <path
              fillRule="evenodd"
              d="M4.293 4.293a1 1 0 011.414 0L10 8.586l4.293-4.293a1 1 0 111.414 1.414L11.414 10l4.293 4.293a1 1 0 01-1.414 1.414L10 11.414l-4.293 4.293a1 1 0 01-1.414-1.414L8.586 10 4.293 5.707a1 1 0 010-1.414z"
              clipRule="evenodd"
            />
          </svg>
        </button>
      </div>
    </div>
  );
}
