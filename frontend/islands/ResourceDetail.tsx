import { useSignal } from "@preact/signals";
import { useCallback, useEffect, useMemo, useRef } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiGet } from "@/lib/api.ts";
import { RESOURCE_API_KINDS, RESOURCE_DETAIL_PATHS } from "@/lib/constants.ts";
import {
  EVENT_DELETED,
  EVENT_MODIFIED,
  EVENT_RESYNC,
  subscribe,
} from "@/lib/ws.ts";
import { selectedNamespace } from "@/lib/namespace.ts";
import { Tabs } from "@/components/ui/Tabs.tsx";
import { CodeBlock } from "@/components/ui/CodeBlock.tsx";
import { ResourceIcon } from "@/components/k8s/ResourceIcon.tsx";
import { age } from "@/lib/format.ts";
import type { K8sEvent, K8sResource } from "@/lib/k8s-types.ts";
import { getOverviewComponent } from "@/components/k8s/detail/index.tsx";
import { MetadataSection } from "@/components/k8s/detail/MetadataSection.tsx";
import { stringify } from "yaml";

interface ResourceDetailProps {
  kind: string;
  name: string;
  namespace?: string;
  clusterScoped?: boolean;
  title: string;
}

const VALID_TABS = new Set(["overview", "yaml", "events", "metrics"]);

function pluralize(s: string): string {
  if (s.endsWith("y") && !s.endsWith("ey")) return s.slice(0, -1) + "ies";
  if (
    s.endsWith("s") || s.endsWith("x") || s.endsWith("ch") || s.endsWith("sh")
  ) return s + "es";
  return s + "s";
}

export default function ResourceDetail({
  kind,
  name,
  namespace,
  clusterScoped = false,
  title,
}: ResourceDetailProps) {
  const resource = useSignal<K8sResource | null>(null);
  const loading = useSignal(true);
  const error = useSignal<string | null>(null);
  const deleted = useSignal(false);
  const updated = useSignal(false);
  const activeTab = useSignal("overview");

  // Events tab state
  const events = useSignal<K8sEvent[]>([]);
  const eventsLoading = useSignal(false);
  const eventsError = useSignal<string | null>(null);
  const eventsFetched = useRef(false);

  // YAML options
  const showManagedFields = useSignal(false);

  // Read initial tab from URL hash
  useEffect(() => {
    if (!IS_BROWSER) return;
    const hash = globalThis.location.hash.replace("#", "");
    if (hash && VALID_TABS.has(hash)) {
      activeTab.value = hash;
    }
  }, []);

  // Set document title
  useEffect(() => {
    if (!IS_BROWSER) return;
    document.title = `${name} - ${title} - KubeCenter`;
    return () => {
      document.title = "KubeCenter";
    };
  }, [name, title]);

  // Navigate to list page when namespace selector changes
  useEffect(() => {
    if (!IS_BROWSER || clusterScoped) return;
    const listPath = RESOURCE_DETAIL_PATHS[kind];
    if (!listPath) return;

    // Store the current namespace at subscription time
    const initialNs = selectedNamespace.value;
    const unsubscribe = selectedNamespace.subscribe((newNs) => {
      if (newNs !== initialNs) {
        globalThis.location.href = listPath;
      }
    });
    return unsubscribe;
  }, [kind, clusterScoped]);

  // Fetch the resource
  const fetchResource = useCallback(async () => {
    loading.value = true;
    error.value = null;
    try {
      const path = namespace
        ? `/v1/resources/${kind}/${namespace}/${name}`
        : `/v1/resources/${kind}/${name}`;
      const res = await apiGet<K8sResource>(path);
      resource.value = res.data;
      updated.value = false;
    } catch (err) {
      if (err instanceof Error && err.message.includes("404")) {
        error.value = `${title} "${name}" not found`;
      } else if (err instanceof Error && err.message.includes("403")) {
        error.value =
          `You don't have permission to view this ${title.toLowerCase()}`;
      } else {
        error.value = err instanceof Error
          ? err.message
          : "Failed to load resource";
      }
    } finally {
      loading.value = false;
    }
  }, [kind, name, namespace, title]);

  // Subscribe to WS and fetch resource
  useEffect(() => {
    if (!IS_BROWSER) return;

    // Don't subscribe WS for secrets
    const enableWS = kind !== "secrets";
    let unsubscribe: (() => void) | undefined;

    if (enableWS) {
      const subId = `detail-${kind}-${namespace || "cluster"}-${name}`;
      unsubscribe = subscribe(
        subId,
        kind,
        namespace ?? "",
        (eventType, object) => {
          if (!object || typeof object !== "object") return;
          const obj = object as K8sResource;

          // Only process events for this specific resource
          if (
            resource.value && obj.metadata?.uid !== resource.value.metadata.uid
          ) {
            return;
          }

          switch (eventType) {
            case EVENT_MODIFIED:
              // Show "updated" banner instead of auto-refreshing YAML
              if (activeTab.value === "yaml") {
                updated.value = true;
              } else {
                resource.value = obj;
              }
              break;
            case EVENT_DELETED:
              deleted.value = true;
              break;
            case EVENT_RESYNC:
              fetchResource();
              break;
          }
        },
      );
    }

    fetchResource();

    return () => {
      unsubscribe?.();
    };
  }, [kind, name, namespace]);

  // Fetch events when Events tab is first activated
  const fetchEvents = useCallback(async () => {
    if (eventsFetched.current) return;
    eventsFetched.current = true;
    eventsLoading.value = true;
    eventsError.value = null;
    try {
      // For cluster-scoped resources, fetch events from all namespaces
      const eventsPath = namespace
        ? `/v1/resources/events/${namespace}`
        : `/v1/resources/events`;
      const res = await apiGet<K8sEvent[]>(eventsPath);
      const allEvents = Array.isArray(res.data) ? res.data : [];

      // Filter by involvedObject kind and name
      const apiKind = RESOURCE_API_KINDS[kind] ?? title;
      events.value = allEvents.filter(
        (e) =>
          e.involvedObject?.kind === apiKind &&
          e.involvedObject?.name === name,
      );
    } catch (err) {
      eventsError.value = err instanceof Error
        ? err.message
        : "Failed to load events";
    } finally {
      eventsLoading.value = false;
    }
  }, [kind, name, namespace, title]);

  // Tab change handler
  const handleTabChange = useCallback(
    (tabId: string) => {
      activeTab.value = tabId;
      if (IS_BROWSER) {
        history.replaceState(null, "", `#${tabId}`);
      }
      if (tabId === "events") {
        fetchEvents();
      }
    },
    [fetchEvents],
  );

  // Generate YAML from resource — memoized to avoid re-stringify on unrelated renders
  const yamlContent = useMemo(() => {
    if (!resource.value) return "";
    const obj = structuredClone(resource.value);
    if (!showManagedFields.value) {
      delete (obj.metadata as Record<string, unknown>).managedFields;
    }
    try {
      return stringify(obj, { lineWidth: 0 });
    } catch {
      return JSON.stringify(obj, null, 2);
    }
  }, [resource.value, showManagedFields.value]);

  // Build back-to-list URL
  const listUrl = RESOURCE_DETAIL_PATHS[kind] ?? "/";

  const tabDefs = [
    {
      id: "overview",
      label: "Overview",
      content: () => {
        if (loading.value) {
          return <LoadingSpinner />;
        }
        if (!resource.value) return null;
        const OverviewComponent = getOverviewComponent(kind);
        return (
          <div class="space-y-6 p-6">
            <MetadataSection resource={resource.value} />
            <OverviewComponent resource={resource.value} />
          </div>
        );
      },
    },
    {
      id: "yaml",
      label: "YAML",
      content: () => {
        if (loading.value || !resource.value) {
          return <LoadingSpinner />;
        }
        return (
          <div class="p-6 space-y-4">
            {updated.value && (
              <div class="flex items-center gap-3 rounded-md border border-blue-200 bg-blue-50 px-4 py-2 text-sm text-blue-700 dark:border-blue-800 dark:bg-blue-900/20 dark:text-blue-400">
                Resource was updated externally.
                <button
                  type="button"
                  onClick={() => fetchResource()}
                  class="font-medium underline hover:no-underline"
                >
                  Refresh
                </button>
              </div>
            )}
            <div class="flex items-center gap-3">
              <label class="flex items-center gap-2 text-sm text-slate-600 dark:text-slate-400">
                <input
                  type="checkbox"
                  checked={showManagedFields.value}
                  onChange={(e) => {
                    showManagedFields.value =
                      (e.target as HTMLInputElement).checked;
                  }}
                  class="rounded border-slate-300"
                />
                Show managed fields
              </label>
            </div>
            <CodeBlock code={yamlContent} language="yaml" />
          </div>
        );
      },
    },
    {
      id: "events",
      label: "Events",
      content: () => {
        if (eventsLoading.value) {
          return <LoadingSpinner />;
        }
        if (eventsError.value) {
          return (
            <div class="p-6">
              <ErrorBanner message={eventsError.value} />
            </div>
          );
        }
        if (events.value.length === 0) {
          return (
            <div class="p-12 text-center text-sm text-slate-400 dark:text-slate-500">
              No events found for this resource
            </div>
          );
        }
        return (
          <div class="p-6">
            <EventsTable events={events.value} />
          </div>
        );
      },
    },
    {
      id: "metrics",
      label: "Metrics",
      content: () => (
        <div class="p-12 text-center text-sm text-slate-400 dark:text-slate-500">
          <p class="text-lg font-medium text-slate-500 dark:text-slate-400">
            Metrics
          </p>
          <p class="mt-1">
            Coming in Step 9 — Prometheus & Grafana integration
          </p>
        </div>
      ),
    },
  ];

  return (
    <div class="space-y-4">
      {/* Deleted banner */}
      {deleted.value && (
        <div class="rounded-md border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-700 dark:border-amber-800 dark:bg-amber-900/20 dark:text-amber-400">
          This {title.toLowerCase()} was deleted.{" "}
          <a href={listUrl} class="font-medium underline hover:no-underline">
            Back to {title.toLowerCase()} list
          </a>
        </div>
      )}

      {/* Header */}
      <div class="flex items-center justify-between">
        <div>
          <div class="flex items-center gap-2 text-sm text-slate-500 dark:text-slate-400 mb-1">
            <a
              href={listUrl}
              class="hover:text-slate-700 dark:hover:text-slate-200"
            >
              {pluralize(title)}
            </a>
            <span>/</span>
            {namespace && (
              <>
                <span>{namespace}</span>
                <span>/</span>
              </>
            )}
            <span class="text-slate-700 dark:text-slate-200">{name}</span>
          </div>
          <div class="flex items-center gap-3">
            <ResourceIcon kind={kind} size={24} class="text-slate-500" />
            <h1 class="text-xl font-semibold text-slate-900 dark:text-white">
              {name}
            </h1>
            {resource.value && (
              <span class="text-sm text-slate-400 dark:text-slate-500">
                {age(resource.value.metadata.creationTimestamp)}
              </span>
            )}
          </div>
        </div>
      </div>

      {/* Error state */}
      {error.value && !resource.value && <ErrorBanner message={error.value} />}

      {/* Tab content */}
      <div class="rounded-lg border border-slate-200 bg-white dark:border-slate-700 dark:bg-slate-800">
        <Tabs
          tabs={tabDefs}
          activeTab={activeTab.value}
          onTabChange={handleTabChange}
        />
      </div>
    </div>
  );
}

function LoadingSpinner() {
  return (
    <div class="flex items-center justify-center p-12">
      <svg
        class="h-6 w-6 animate-spin text-slate-400"
        viewBox="0 0 24 24"
        fill="none"
      >
        <circle
          class="opacity-25"
          cx="12"
          cy="12"
          r="10"
          stroke="currentColor"
          stroke-width="4"
        />
        <path
          class="opacity-75"
          fill="currentColor"
          d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z"
        />
      </svg>
    </div>
  );
}

function ErrorBanner({ message }: { message: string }) {
  return (
    <div class="rounded-md border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-800 dark:bg-red-900/20 dark:text-red-400">
      {message}
    </div>
  );
}

function EventsTable({ events }: { events: K8sEvent[] }) {
  return (
    <div class="overflow-x-auto">
      <table class="w-full text-sm">
        <thead>
          <tr class="border-b border-slate-200 dark:border-slate-700">
            <th class="px-4 py-2 text-left text-xs font-medium uppercase text-slate-500">
              Type
            </th>
            <th class="px-4 py-2 text-left text-xs font-medium uppercase text-slate-500">
              Reason
            </th>
            <th class="px-4 py-2 text-left text-xs font-medium uppercase text-slate-500">
              Message
            </th>
            <th class="px-4 py-2 text-left text-xs font-medium uppercase text-slate-500">
              Count
            </th>
            <th class="px-4 py-2 text-left text-xs font-medium uppercase text-slate-500">
              Last Seen
            </th>
          </tr>
        </thead>
        <tbody class="divide-y divide-slate-100 dark:divide-slate-700/50">
          {events.map((e) => (
            <tr key={e.metadata.uid}>
              <td class="px-4 py-2">
                <span
                  class={`inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium ring-1 ring-inset ${
                    e.type === "Warning"
                      ? "bg-amber-50 text-amber-700 ring-amber-600/20 dark:bg-amber-500/10 dark:text-amber-400"
                      : "bg-slate-50 text-slate-600 ring-slate-500/20 dark:bg-slate-500/10 dark:text-slate-400"
                  }`}
                >
                  {e.type ?? "Normal"}
                </span>
              </td>
              <td class="px-4 py-2 text-slate-700 dark:text-slate-300">
                {e.reason ?? "-"}
              </td>
              <td class="px-4 py-2 text-slate-600 dark:text-slate-400 max-w-md truncate">
                {e.message ?? "-"}
              </td>
              <td class="px-4 py-2 text-slate-600 dark:text-slate-400">
                {e.count ?? 1}
              </td>
              <td class="px-4 py-2 text-slate-500 dark:text-slate-500">
                {e.lastTimestamp ? age(e.lastTimestamp) : "-"}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
