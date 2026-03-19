import { useSignal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiGet } from "@/lib/api.ts";
import {
  EVENT_ADDED,
  EVENT_DELETED,
  EVENT_RESYNC,
  subscribe,
} from "@/lib/ws.ts";
import type { AlertEvent } from "@/lib/k8s-types.ts";

export default function AlertBanner() {
  const alerts = useSignal<AlertEvent[]>([]);
  const dismissed = useSignal(false);

  function fetchAlerts() {
    apiGet<AlertEvent[]>("/v1/alerts")
      .then((res) => {
        alerts.value = res.data ?? [];
      })
      .catch(() => {
        // Silently fail — banner is non-critical
      });
  }

  useEffect(() => {
    if (!IS_BROWSER) return;

    // Fetch initial state via REST
    fetchAlerts();

    // Subscribe to real-time alert events via WebSocket.
    // "alerts" is in alwaysAllowKinds — JWT auth only, no RBAC check.
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
          const exists = alerts.value.some(
            (a) => a.fingerprint === alert.fingerprint,
          );
          if (!exists) {
            alerts.value = [...alerts.value, alert];
          }
        } else if (eventType === EVENT_DELETED) {
          // Remove resolved alert
          alerts.value = alerts.value.filter(
            (a) => a.fingerprint !== alert.fingerprint,
          );
        }
      },
    );

    return unsubscribe;
  }, []);

  if (!IS_BROWSER || dismissed.value || alerts.value.length === 0) {
    return null;
  }

  const critical = alerts.value.filter((a) => a.severity === "critical").length;
  const warning = alerts.value.filter((a) => a.severity === "warning").length;
  const total = alerts.value.length;

  return (
    <div class="bg-red-50 dark:bg-red-900/20 border-b border-red-200 dark:border-red-800 px-4 py-2">
      <div class="flex items-center justify-between max-w-7xl mx-auto">
        <a
          href="/alerting"
          class="flex items-center gap-2 text-sm text-red-700 dark:text-red-400 hover:text-red-900 dark:hover:text-red-300"
        >
          <svg
            class="w-4 h-4"
            fill="none"
            stroke="currentColor"
            viewBox="0 0 24 24"
          >
            <path
              stroke-linecap="round"
              stroke-linejoin="round"
              stroke-width="2"
              d="M15 17h5l-1.405-1.405A2.032 2.032 0 0118 14.158V11a6.002 6.002 0 00-4-5.659V5a2 2 0 10-4 0v.341C7.67 6.165 6 8.388 6 11v3.159c0 .538-.214 1.055-.595 1.436L4 17h5m6 0v1a3 3 0 11-6 0v-1m6 0H9"
            />
          </svg>
          <span class="font-medium">
            {total} active alert{total !== 1 ? "s" : ""}
          </span>
          {critical > 0 && (
            <span class="bg-red-200 dark:bg-red-800 text-red-800 dark:text-red-200 text-xs px-1.5 py-0.5 rounded-full">
              {critical} critical
            </span>
          )}
          {warning > 0 && (
            <span class="bg-amber-200 dark:bg-amber-800 text-amber-800 dark:text-amber-200 text-xs px-1.5 py-0.5 rounded-full">
              {warning} warning
            </span>
          )}
        </a>
        <button
          type="button"
          onClick={() => dismissed.value = true}
          class="text-red-400 hover:text-red-600 dark:text-red-500 dark:hover:text-red-400"
          aria-label="Dismiss"
        >
          <svg class="w-4 h-4" fill="currentColor" viewBox="0 0 20 20">
            <path
              fill-rule="evenodd"
              d="M4.293 4.293a1 1 0 011.414 0L10 8.586l4.293-4.293a1 1 0 111.414 1.414L11.414 10l4.293 4.293a1 1 0 01-1.414 1.414L10 11.414l-4.293 4.293a1 1 0 01-1.414-1.414L8.586 10 4.293 5.707a1 1 0 010-1.414z"
              clip-rule="evenodd"
            />
          </svg>
        </button>
      </div>
    </div>
  );
}
