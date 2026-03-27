import { useComputed, useSignal } from "@preact/signals";
import { useCallback, useEffect, useMemo, useRef } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiGet, apiPostRaw } from "@/lib/api.ts";
import { RESOURCE_API_KINDS, RESOURCE_DETAIL_PATHS } from "@/lib/constants.ts";
import {
  type ActionId,
  executeAction,
  getActionMeta,
  getVisibleActions,
} from "@/lib/action-handlers.ts";
import { useAuth } from "@/lib/auth.ts";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog.tsx";
import { showToast } from "@/islands/ToastProvider.tsx";
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
import PerformancePanel from "@/islands/PerformancePanel.tsx";
import LogViewer from "@/islands/LogViewer.tsx";
import PodTerminal from "@/islands/PodTerminal.tsx";
import RelatedPods from "@/islands/RelatedPods.tsx";
import SplitPane from "@/islands/SplitPane.tsx";
import RoleBindingsList from "@/islands/RoleBindingsList.tsx";
import { CodeMirrorEditor } from "@/components/ui/CodeMirrorEditor.tsx";

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

  // Action buttons state
  const { rbac } = useAuth();
  const actionLoading = useSignal(false);
  const confirmAction = useSignal<
    {
      actionId: ActionId;
      params?: Record<string, unknown>;
    } | null
  >(null);
  const scaleTarget = useSignal(false);
  const scaleValue = useSignal(1);

  const actions = useComputed(() =>
    getVisibleActions(kind, namespace ?? "", rbac.value)
  );

  const runAction = async (
    actionId: ActionId,
    params?: Record<string, unknown>,
  ) => {
    if (actionLoading.value) return;
    actionLoading.value = true;
    try {
      const message = await executeAction(
        actionId,
        kind,
        namespace ?? "",
        name,
        params,
      );
      showToast(message, "success");
      confirmAction.value = null;
      scaleTarget.value = false;
    } catch (err) {
      const msg = err instanceof Error ? err.message : "Action failed";
      showToast(msg, "error");
    } finally {
      actionLoading.value = false;
    }
  };

  const handleAction = (actionId: ActionId) => {
    if (actionLoading.value || !resource.value) return;
    const meta = getActionMeta(actionId, resource.value);

    if (actionId === "scale") {
      const spec = resource.value.spec as { replicas?: number } | undefined;
      scaleValue.value = spec?.replicas ?? 1;
      scaleTarget.value = true;
      return;
    }

    if (meta.confirm) {
      let params: Record<string, unknown> | undefined;
      if (actionId === "suspend") {
        const spec = resource.value.spec as { suspend?: boolean } | undefined;
        params = { suspend: !spec?.suspend };
      }
      confirmAction.value = { actionId, params };
      return;
    }

    runAction(actionId);
  };

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
        error.value = `${title}"${name}" not found`;
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
              // Show"updated" banner instead of auto-refreshing YAML
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
  void tick.value;

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
            {kind !== "deployments" && (
              <MetadataSection resource={resource.value} />
            )}
            <OverviewComponent resource={resource.value} />
            {(kind === "roles" || kind === "clusterroles") && (
              <RoleBindingsList
                roleName={name}
                roleKind={kind === "clusterroles" ? "ClusterRole" : "Role"}
                namespace={namespace}
              />
            )}
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
            {/* Updated externally banner */}
            {updated.value && (
              <div class="flex items-center gap-3 rounded-md border border-info/30 bg-info/10 px-4 py-2 text-sm text-info">
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
              <div class="flex items-center gap-3 rounded-md border border-success/30 bg-success-dim px-4 py-2 text-sm text-success">
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
              <div class="rounded-md border border-danger/30 bg-danger-dim px-4 py-3 text-sm text-danger">
                <p class="font-medium">Apply failed</p>
                <p class="mt-1">{yamlApplyError.value}</p>
              </div>
            )}

            {/* Toolbar */}
            <div class="flex items-center justify-between">
              <div class="flex items-center gap-3">
                <label class="flex items-center gap-2 text-sm text-text-secondary">
                  <input
                    type="checkbox"
                    checked={showManagedFields.value}
                    onChange={(e) => {
                      showManagedFields.value =
                        (e.target as HTMLInputElement).checked;
                    }}
                    class="rounded border-border-primary"
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
                        class="inline-flex items-center gap-1.5 rounded-md border border-border-primary bg-surface px-3 py-1.5 text-sm font-medium text-text-secondary hover:bg-hover disabled:cursor-not-allowed disabled:opacity-50"
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
                        class="inline-flex items-center gap-1.5 rounded-md border border-border-primary bg-surface px-3 py-1.5 text-sm font-medium text-text-secondary hover:bg-hover disabled:cursor-not-allowed disabled:opacity-50"
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
                        disabled={yamlApplying.value ||
                          yamlEditContent.value === yamlContent}
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
                        class="inline-flex items-center gap-1.5 rounded-md border border-border-primary bg-surface px-3 py-1.5 text-sm font-medium text-text-secondary hover:bg-hover disabled:cursor-not-allowed disabled:opacity-50"
                      >
                        Discard
                      </button>
                    </>
                  )}
              </div>
            </div>

            {/* CodeMirror YAML editor — native DOM, works inline */}
            <div class="rounded-md border border-border-primary overflow-hidden">
              <CodeMirrorEditor
                value={yamlEditing.value ? yamlEditContent.value : yamlContent}
                onChange={yamlEditing.value
                  ? (v) => {
                    yamlEditContent.value = v;
                  }
                  : undefined}
                readOnly={!yamlEditing.value}
              />
            </div>
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
            <div class="p-12 text-center text-sm text-text-muted">
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

  // Compute pod selector for workload kinds (used in split pane)
  const isWorkload = ["deployments", "statefulsets", "daemonsets"].includes(
    kind,
  );
  const podSelector = (() => {
    if (!isWorkload || !resource.value || !namespace) return "";
    // deno-lint-ignore no-explicit-any
    const res = resource.value as any;
    const matchLabels = res?.spec?.selector?.matchLabels ?? {};
    return Object.entries(matchLabels).map(([k, v]) => `${k}=${v}`).join(",");
  })();

  // Add Logs and Exec tabs for pods
  if (kind === "pods" && namespace && IS_BROWSER) {
    tabDefs.push({
      id: "logs",
      label: "Logs",
      content: () => {
        // deno-lint-ignore no-explicit-any
        const res = resource.value as any;
        const containers: string[] =
          // deno-lint-ignore no-explicit-any
          res?.spec?.containers?.map((c: any) => c.name) ?? [];
        const initContainers: string[] =
          // deno-lint-ignore no-explicit-any
          res?.spec?.initContainers?.map((c: any) => c.name) ?? [];
        return (
          <LogViewer
            namespace={namespace}
            pod={name}
            containers={containers.length > 0 ? containers : ["default"]}
            initContainers={initContainers}
          />
        );
      },
    });
    tabDefs.push({
      id: "terminal",
      label: "Terminal",
      content: () => {
        // deno-lint-ignore no-explicit-any
        const res = resource.value as any;
        const containers: string[] =
          // deno-lint-ignore no-explicit-any
          res?.spec?.containers?.map((c: any) => c.name) ?? [];
        return (
          <PodTerminal
            namespace={namespace}
            name={name}
            containers={containers.length > 0 ? containers : ["default"]}
          />
        );
      },
    });
  }

  return (
    <div class="space-y-4">
      {/* Deleted banner */}
      {deleted.value && (
        <div class="rounded-md border border-warning/30 bg-warning-dim px-4 py-3 text-sm text-warning">
          This {title.toLowerCase()} was deleted.{""}
          <a href={listUrl} class="font-medium underline hover:no-underline">
            Back to {title.toLowerCase()} list
          </a>
        </div>
      )}

      {/* Header */}
      <div
        style={{
          display: "flex",
          alignItems: "flex-start",
          gap: "14px",
        }}
      >
        {/* Icon */}
        <div
          style={{
            width: "44px",
            height: "44px",
            borderRadius: "var(--radius)",
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
            flexShrink: 0,
            background:
              "linear-gradient(135deg, rgba(0, 194, 255, 0.15), rgba(0, 194, 255, 0.05))",
            border: "1px solid rgba(0, 194, 255, 0.3)",
            color: "var(--accent)",
          }}
        >
          <ResourceIcon kind={kind} size={22} />
        </div>
        {/* Title section */}
        <div style={{ flex: 1 }}>
          <h1
            style={{
              fontSize: "18px",
              fontWeight: 600,
              fontFamily: "var(--font-mono)",
              letterSpacing: "-0.02em",
              margin: 0,
            }}
          >
            {name}
          </h1>
          <div
            style={{
              display: "flex",
              alignItems: "center",
              gap: "6px",
              fontSize: "12px",
              color: "var(--text-muted)",
              marginTop: "4px",
            }}
          >
            <a
              href={listUrl}
              style={{
                color: "var(--text-muted)",
                textDecoration: "none",
              }}
              onMouseOver={(e) => {
                (e.currentTarget as HTMLElement).style.color = "var(--accent)";
              }}
              onMouseOut={(e) => {
                (e.currentTarget as HTMLElement).style.color =
                  "var(--text-muted)";
              }}
            >
              {pluralize(title)}
            </a>
            <span style={{ opacity: 0.5 }}>/</span>
            {namespace && (
              <>
                <span style={{ color: "var(--text-secondary)" }}>
                  {namespace}
                </span>
                <span style={{ opacity: 0.5 }}>/</span>
              </>
            )}
            <span style={{ color: "var(--text-secondary)" }}>{name}</span>
          </div>
        </div>
        {/* Action buttons */}
        {resource.value && actions.value.length > 0 && (
          <div style={{ display: "flex", gap: "6px" }}>
            {actions.value.map((actionId) => {
              const meta = getActionMeta(actionId, resource.value!);
              const isDanger = !!meta.danger;
              return (
                <button
                  key={actionId}
                  type="button"
                  onClick={() => handleAction(actionId)}
                  disabled={actionLoading.value}
                  style={{
                    padding: "5px 10px",
                    borderRadius: "var(--radius-sm)",
                    fontSize: "12px",
                    fontWeight: 500,
                    border: isDanger
                      ? "1px solid rgba(255, 82, 82, 0.3)"
                      : "1px solid var(--border-primary)",
                    background: "transparent",
                    color: isDanger ? "var(--error)" : "var(--text-secondary)",
                    cursor: "pointer",
                    display: "flex",
                    alignItems: "center",
                    gap: "5px",
                    opacity: actionLoading.value ? 0.5 : 1,
                  }}
                  onMouseOver={(e) => {
                    (e.currentTarget as HTMLElement).style.background = isDanger
                      ? "var(--error-dim)"
                      : "var(--bg-elevated)";
                  }}
                  onMouseOut={(e) => {
                    (e.currentTarget as HTMLElement).style.background =
                      "transparent";
                  }}
                >
                  <ActionIcon actionId={actionId} />
                  {meta.label}
                </button>
              );
            })}
          </div>
        )}
      </div>

      {/* Error state */}
      {error.value && !resource.value && <ErrorBanner message={error.value} />}

      {/* Tab content */}
      {isWorkload && namespace && podSelector
        ? (
          <div
            style={{
              display: "flex",
              flexDirection: "column",
              flex: 1,
              minHeight: 0,
            }}
          >
            <SplitPane
              defaultRatio={0.6}
              left={
                <div class="rounded-lg border border-border-primary bg-surface">
                  <Tabs
                    tabs={tabDefs}
                    activeTab={activeTab.value}
                    onTabChange={handleTabChange}
                  />
                </div>
              }
              right={
                <div style={{ padding: "16px" }}>
                  <RelatedPods
                    namespace={namespace}
                    labelSelector={podSelector}
                    parentName={name}
                  />
                </div>
              }
            />
          </div>
        )
        : (
          <div class="rounded-lg border border-border-primary bg-surface">
            <Tabs
              tabs={tabDefs}
              activeTab={activeTab.value}
              onTabChange={handleTabChange}
            />
          </div>
        )}

      {/* Confirm dialog */}
      {confirmAction.value && resource.value && (() => {
        const meta = getActionMeta(
          confirmAction.value!.actionId,
          resource.value!,
        );
        const isDestructive = meta.confirm === "destructive";
        return (
          <ConfirmDialog
            title={`${meta.label} ${name}`}
            message={meta.confirmMessage}
            confirmLabel={meta.label}
            danger={meta.danger}
            typeToConfirm={isDestructive ? name : undefined}
            onConfirm={() =>
              runAction(
                confirmAction.value!.actionId,
                confirmAction.value!.params,
              )}
            onCancel={() => {
              confirmAction.value = null;
            }}
          />
        );
      })()}

      {/* Scale dialog */}
      {scaleTarget.value && (
        <ConfirmDialog
          title={`Scale ${name}`}
          message={
            <div class="space-y-3">
              <p>Set the number of replicas:</p>
              <input
                type="number"
                min="0"
                max="100"
                value={scaleValue.value}
                onInput={(e) => {
                  scaleValue.value = parseInt(
                    (e.target as HTMLInputElement).value,
                    10,
                  );
                }}
                class="w-full rounded-md border border-border-primary bg-surface px-3 py-2 text-sm text-text-primary"
              />
            </div>
          }
          confirmLabel="Scale"
          onConfirm={() => runAction("scale", { replicas: scaleValue.value })}
          onCancel={() => {
            scaleTarget.value = false;
          }}
        />
      )}
    </div>
  );
}

function ActionIcon({ actionId }: { actionId: ActionId }) {
  const svgProps = {
    width: 13,
    height: 13,
    viewBox: "0 0 14 14",
    fill: "none",
    stroke: "currentColor",
    "stroke-width": "1.5",
  };
  switch (actionId) {
    case "scale":
      return (
        <svg {...svgProps}>
          <path d="M7 3v8M3 7h8" />
        </svg>
      );
    case "restart":
      return (
        <svg {...svgProps}>
          <path d="M2 7a5 5 0 019.5-1.5M12 7a5 5 0 01-9.5 1.5" />
        </svg>
      );
    case "delete":
      return (
        <svg {...svgProps}>
          <path d="M3 4h8M5 4V3h4v1M4 4v7a1 1 0 001 1h4a1 1 0 001-1V4" />
        </svg>
      );
    case "suspend":
      return (
        <svg {...svgProps}>
          <rect x="3" y="3" width="3" height="8" rx="0.5" />
          <rect x="8" y="3" width="3" height="8" rx="0.5" />
        </svg>
      );
    case "trigger":
      return (
        <svg {...svgProps}>
          <path d="M5 3l6 4-6 4V3z" />
        </svg>
      );
    default:
      return null;
  }
}

function EventsTable({ events }: { events: K8sEvent[] }) {
  return (
    <div class="overflow-x-auto">
      <table class="w-full text-sm">
        <thead>
          <tr class="border-b border-border-primary">
            <th class="px-4 py-2 text-left text-xs font-medium uppercase text-text-muted">
              Type
            </th>
            <th class="px-4 py-2 text-left text-xs font-medium uppercase text-text-muted">
              Reason
            </th>
            <th class="px-4 py-2 text-left text-xs font-medium uppercase text-text-muted">
              Message
            </th>
            <th class="px-4 py-2 text-left text-xs font-medium uppercase text-text-muted">
              Count
            </th>
            <th class="px-4 py-2 text-left text-xs font-medium uppercase text-text-muted">
              Last Seen
            </th>
          </tr>
        </thead>
        <tbody class="divide-y divide-border-subtle">
          {events.map((e) => (
            <tr key={e.metadata.uid}>
              <td class="px-4 py-2">
                <span
                  class={`inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium ring-1 ring-inset ${
                    e.type === "Warning"
                      ? "bg-warning-dim text-warning ring-warning/20"
                      : "bg-elevated text-text-secondary ring-border-primary"
                  }`}
                >
                  {e.type ?? "Normal"}
                </span>
              </td>
              <td class="px-4 py-2 text-text-secondary">
                {e.reason ?? "-"}
              </td>
              <td class="px-4 py-2 text-text-secondary max-w-md truncate">
                {e.message ?? "-"}
              </td>
              <td class="px-4 py-2 text-text-secondary">
                {e.count ?? 1}
              </td>
              <td class="px-4 py-2 text-text-muted">
                {e.lastTimestamp ? age(e.lastTimestamp) : "-"}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
