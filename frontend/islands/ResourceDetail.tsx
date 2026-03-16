import { useSignal } from "@preact/signals";
import { useCallback, useEffect, useMemo, useRef } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiGet, apiPostRaw } from "@/lib/api.ts";
import { RESOURCE_API_KINDS, RESOURCE_DETAIL_PATHS } from "@/lib/constants.ts";
import {
  EVENT_DELETED,
  EVENT_MODIFIED,
  EVENT_RESYNC,
  subscribe,
} from "@/lib/ws.ts";
import { selectedNamespace } from "@/lib/namespace.ts";
import { Tabs } from "@/components/ui/Tabs.tsx";
import { LoadingSpinner } from "@/components/ui/LoadingSpinner.tsx";
import { ErrorBanner } from "@/components/ui/ErrorBanner.tsx";
import { ResourceIcon } from "@/components/k8s/ResourceIcon.tsx";
import { age } from "@/lib/format.ts";
import type { K8sEvent, K8sResource } from "@/lib/k8s-types.ts";
import { getOverviewComponent } from "@/components/k8s/detail/index.tsx";
import { MetadataSection } from "@/components/k8s/detail/MetadataSection.tsx";
import { stringify } from "yaml";
import YamlEditor from "@/islands/YamlEditor.tsx";
import PerformancePanel from "@/islands/PerformancePanel.tsx";
import LogViewer from "@/islands/LogViewer.tsx";

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

  // YAML edit state
  const yamlEditing = useSignal(false);
  const yamlEditContent = useSignal("");
  const yamlApplying = useSignal(false);
  const yamlApplyError = useSignal<string | null>(null);
  const yamlApplySuccess = useSignal(false);
  const isSecret = kind === "secrets";

  // Dirty state navigation guard (D9)
  // Uses a ref to track latest yamlContent so the handler always has current state,
  // while only registering the event listener once.
  const yamlContentRef = useRef("");
  useEffect(() => {
    if (!IS_BROWSER) return;
    const handler = (e: BeforeUnloadEvent) => {
      if (
        yamlEditing.value &&
        yamlEditContent.value !== yamlContentRef.current
      ) {
        e.preventDefault();
      }
    };
    globalThis.addEventListener("beforeunload", handler);
    return () => globalThis.removeEventListener("beforeunload", handler);
  }, []);

  // Periodic tick to refresh age displays (every 30s)
  const tick = useSignal(0);
  useEffect(() => {
    if (!IS_BROWSER) return;
    const id = setInterval(() => {
      tick.value = tick.value + 1;
    }, 30_000);
    return () => clearInterval(id);
  }, []);

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
    document.title = `${name} - ${title} - k8sCenter`;
    return () => {
      document.title = "k8sCenter";
    };
  }, [name, title]);

  // Navigate to list page when namespace selector changes
  useEffect(() => {
    if (!IS_BROWSER || clusterScoped) return;
    const listPath = RESOURCE_DETAIL_PATHS[kind];
    if (!listPath) return;

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
      // Allow events to be re-fetched after a resource refresh
      eventsFetched.current = false;
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

  // Fetch events when Events tab is first activated — server-side filtered
  const fetchEvents = useCallback(async () => {
    if (eventsFetched.current) return;
    eventsFetched.current = true;
    eventsLoading.value = true;
    eventsError.value = null;
    try {
      const apiKind = RESOURCE_API_KINDS[kind] ?? title;
      const params = new URLSearchParams({
        involvedObjectKind: apiKind,
        involvedObjectName: name,
      });
      const eventsPath = namespace
        ? `/v1/resources/events/${namespace}?${params}`
        : `/v1/resources/events?${params}`;
      const res = await apiGet<K8sEvent[]>(eventsPath);
      events.value = Array.isArray(res.data) ? res.data : [];
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

  // Keep ref in sync for the beforeunload handler
  yamlContentRef.current = yamlContent;

  // Build back-to-list URL
  const listUrl = RESOURCE_DETAIL_PATHS[kind] ?? "/";

  // Force age() to use tick for reactivity (read tick.value so signal is tracked)
  const _tick = tick.value;

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

        const isDirty = yamlEditing.value &&
          yamlEditContent.value !== yamlContent;

        return (
          <div class="p-6 space-y-4">
            {/* Updated externally banner */}
            {updated.value && (
              <div class="flex items-center gap-3 rounded-md border border-blue-200 bg-blue-50 px-4 py-2 text-sm text-blue-700 dark:border-blue-800 dark:bg-blue-900/20 dark:text-blue-400">
                Resource was updated externally.
                <button
                  type="button"
                  onClick={() => {
                    fetchResource();
                    yamlEditing.value = false;
                    yamlApplyError.value = null;
                    yamlApplySuccess.value = false;
                  }}
                  class="font-medium underline hover:no-underline"
                >
                  Refresh
                </button>
              </div>
            )}

            {/* Apply success banner */}
            {yamlApplySuccess.value && (
              <div class="flex items-center gap-3 rounded-md border border-green-200 bg-green-50 px-4 py-2 text-sm text-green-700 dark:border-green-800 dark:bg-green-900/20 dark:text-green-400">
                Changes applied successfully.
                <button
                  type="button"
                  onClick={() => {
                    yamlApplySuccess.value = false;
                  }}
                  class="font-medium underline hover:no-underline"
                >
                  Dismiss
                </button>
              </div>
            )}

            {/* Apply error banner */}
            {yamlApplyError.value && (
              <div class="rounded-md border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-800 dark:bg-red-900/20 dark:text-red-400">
                <p class="font-medium">Apply failed</p>
                <p class="mt-1">{yamlApplyError.value}</p>
              </div>
            )}

            {/* Toolbar */}
            <div class="flex items-center justify-between">
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
                    disabled={yamlEditing.value}
                  />
                  Show managed fields
                </label>
              </div>
              <div class="flex items-center gap-2">
                {!yamlEditing.value
                  ? (
                    <>
                      <button
                        type="button"
                        onClick={() => {
                          yamlEditContent.value = yamlContent;
                          yamlEditing.value = true;
                          yamlApplyError.value = null;
                          yamlApplySuccess.value = false;
                        }}
                        disabled={isSecret}
                        title={isSecret
                          ? "Secrets cannot be edited via YAML"
                          : "Edit YAML"}
                        class="inline-flex items-center gap-1.5 rounded-md border border-slate-300 bg-white px-3 py-1.5 text-sm font-medium text-slate-700 hover:bg-slate-50 disabled:cursor-not-allowed disabled:opacity-50 dark:border-slate-600 dark:bg-slate-700 dark:text-slate-200 dark:hover:bg-slate-600"
                      >
                        Edit
                      </button>
                      <button
                        type="button"
                        onClick={async () => {
                          try {
                            const exportPath = namespace
                              ? `/v1/yaml/export/${kind}/${namespace}/${name}`
                              : `/v1/yaml/export/${kind}/_/${name}`;
                            const res = await apiGet<string>(exportPath);
                            const blob = new Blob(
                              [
                                typeof res.data === "string"
                                  ? res.data
                                  : JSON.stringify(res.data, null, 2),
                              ],
                              { type: "text/yaml" },
                            );
                            const url = URL.createObjectURL(blob);
                            const a = document.createElement("a");
                            a.href = url;
                            a.download = `${name}.yaml`;
                            a.click();
                            URL.revokeObjectURL(url);
                          } catch (err) {
                            yamlApplyError.value = err instanceof Error
                              ? err.message
                              : "Export failed";
                          }
                        }}
                        disabled={isSecret}
                        title={isSecret
                          ? "Secrets cannot be exported (values are masked)"
                          : "Export clean YAML"}
                        class="inline-flex items-center gap-1.5 rounded-md border border-slate-300 bg-white px-3 py-1.5 text-sm font-medium text-slate-700 hover:bg-slate-50 disabled:cursor-not-allowed disabled:opacity-50 dark:border-slate-600 dark:bg-slate-700 dark:text-slate-200 dark:hover:bg-slate-600"
                      >
                        Export
                      </button>
                    </>
                  )
                  : (
                    <>
                      <button
                        type="button"
                        onClick={async () => {
                          if (yamlApplying.value) return;
                          yamlApplying.value = true;
                          yamlApplyError.value = null;
                          yamlApplySuccess.value = false;
                          try {
                            await apiPostRaw(
                              "/v1/yaml/apply",
                              yamlEditContent.value,
                            );
                            yamlApplySuccess.value = true;
                            yamlEditing.value = false;
                            await fetchResource();
                          } catch (err) {
                            yamlApplyError.value = err instanceof Error
                              ? err.message
                              : "Apply failed";
                          } finally {
                            yamlApplying.value = false;
                          }
                        }}
                        disabled={yamlApplying.value || !isDirty}
                        class="inline-flex items-center gap-1.5 rounded-md bg-blue-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-50"
                      >
                        {yamlApplying.value ? "Applying..." : "Apply"}
                      </button>
                      <button
                        type="button"
                        onClick={() => {
                          yamlEditing.value = false;
                          yamlEditContent.value = "";
                          yamlApplyError.value = null;
                        }}
                        disabled={yamlApplying.value}
                        class="inline-flex items-center gap-1.5 rounded-md border border-slate-300 bg-white px-3 py-1.5 text-sm font-medium text-slate-700 hover:bg-slate-50 disabled:cursor-not-allowed disabled:opacity-50 dark:border-slate-600 dark:bg-slate-700 dark:text-slate-200 dark:hover:bg-slate-600"
                      >
                        Discard
                      </button>
                    </>
                  )}
              </div>
            </div>

            {/* YAML Editor */}
            <YamlEditor
              value={yamlEditing.value ? yamlEditContent.value : yamlContent}
              onChange={(v) => {
                yamlEditContent.value = v;
              }}
              readOnly={!yamlEditing.value}
              height="500px"
            />
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
        <PerformancePanel kind={kind} name={name} namespace={namespace} />
      ),
    },
  ];

  // Add Logs tab for pods
  if (kind === "pods" && resource.value) {
    const containers: string[] =
      (resource.value as any)?.spec?.containers?.map((c: any) => c.name) ?? [];
    if (containers.length > 0 && namespace) {
      tabs.push({
        id: "logs",
        label: "Logs",
        content: () => (
          <LogViewer namespace={namespace} pod={name} containers={containers} />
        ),
      });
    }
  }

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
                {_tick >= 0 && age(resource.value.metadata.creationTimestamp)}
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
