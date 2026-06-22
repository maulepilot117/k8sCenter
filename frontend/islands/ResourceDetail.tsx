import { useComputed, useSignal } from "@preact/signals";
import { useCallback, useEffect, useMemo, useRef } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiGet, apiPostRaw } from "@/lib/api.ts";
import { useDirtyGuard } from "@/lib/hooks/use-dirty-guard.ts";
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
import { LoadingSpinner } from "@/components/ui/LoadingSpinner.tsx";
import { ErrorBanner } from "@/components/ui/ErrorBanner.tsx";
import { ResourceIcon } from "@/components/k8s/ResourceIcon.tsx";
import { age } from "@/lib/format.ts";
import type {
  DaemonSet,
  Deployment,
  K8sEvent,
  K8sResource,
  Pod,
  StatefulSet,
} from "@/lib/k8s-types.ts";
import { getOverviewComponent } from "@/components/k8s/detail/index.tsx";
import { MetadataSection } from "@/components/k8s/detail/MetadataSection.tsx";
import { stringify } from "yaml";
import PerformancePanel from "@/islands/PerformancePanel.tsx";
import LogViewer from "@/islands/LogViewer.tsx";
import PodTerminal from "@/islands/PodTerminal.tsx";
import RelatedPods from "@/islands/RelatedPods.tsx";
import RoleBindingsList from "@/islands/RoleBindingsList.tsx";
import MetricsRail from "@/islands/MetricsRail.tsx";
import { CodeMirrorEditor } from "@/components/ui/CodeMirrorEditor.tsx";
import DetailShell, { type DetailTab } from "@/components/k8s/DetailShell.tsx";
import type { Tone } from "@/components/ui/glass/StatusBadge.tsx";

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

/** A condition shape shared across Deployment, Job, and other resource statuses. */
interface K8sCondition {
  type: string;
  status: string;
  reason?: string;
}

/** Derive a StatusBadge label+tone from a K8sResource. */
function deriveStatus(
  kind: string,
  resource: K8sResource,
): { label: string; tone: Tone } {
  // Deployments — use Available/Progressing conditions (only Deployment has them typed)
  if (kind === "deployments") {
    const dep = resource as Deployment;
    const conds = dep.status?.conditions ?? [];
    const available = conds.find((c) => c.type === "Available");
    if (available?.status === "True") return { label: "Available", tone: "ok" };
    const progressing = conds.find((c) => c.type === "Progressing");
    if (progressing?.status === "True") {
      return { label: "Progressing", tone: "warn" };
    }
    return { label: available?.reason ?? "Unavailable", tone: "crit" };
  }

  // StatefulSets — derive from readyReplicas vs spec.replicas
  if (kind === "statefulsets") {
    const ss = resource as StatefulSet;
    const ready = ss.status?.readyReplicas ?? 0;
    const desired = ss.spec?.replicas ?? 0;
    if (desired === 0 || ready >= desired) {
      return { label: "Available", tone: "ok" };
    }
    if (ready > 0) return { label: "Progressing", tone: "warn" };
    return { label: "Unavailable", tone: "crit" };
  }

  // DaemonSets — derive from numberReady vs desiredNumberScheduled
  if (kind === "daemonsets") {
    const ds = resource as DaemonSet;
    const ready = ds.status?.numberReady ?? 0;
    const desired = ds.status?.desiredNumberScheduled ?? 0;
    if (desired === 0 || ready >= desired) {
      return { label: "Available", tone: "ok" };
    }
    if (ready > 0) return { label: "Progressing", tone: "warn" };
    return { label: "Unavailable", tone: "crit" };
  }

  // Pods — use phase
  if (kind === "pods") {
    const pod = resource as Pod;
    const phase = pod.status?.phase ?? "Unknown";
    if (phase === "Running") {
      const allReady = pod.status?.containerStatuses?.every((c) => c.ready) ??
        false;
      return allReady
        ? { label: "Running", tone: "ok" }
        : { label: "Not Ready", tone: "warn" };
    }
    if (phase === "Succeeded") return { label: "Succeeded", tone: "ok" };
    if (phase === "Pending") return { label: "Pending", tone: "warn" };
    return { label: phase, tone: "crit" };
  }

  // Jobs — cast status.conditions since K8sResource.status is typed as {}
  if (kind === "jobs") {
    const conds =
      (resource.status as { conditions?: K8sCondition[] } | undefined)
        ?.conditions ?? [];
    if (conds.find((c) => c.type === "Complete" && c.status === "True")) {
      return { label: "Complete", tone: "ok" };
    }
    if (conds.find((c) => c.type === "Failed" && c.status === "True")) {
      return { label: "Failed", tone: "crit" };
    }
    return { label: "Active", tone: "warn" };
  }

  // CronJobs
  if (kind === "cronjobs") {
    const suspended = (resource.spec as { suspend?: boolean } | undefined)
      ?.suspend;
    if (suspended) return { label: "Suspended", tone: "warn" };
    return { label: "Active", tone: "ok" };
  }

  return { label: "Ready", tone: "ok" };
}

/** Build subtitle: "Deployment · namespace prod · 6/6 replicas" */
function deriveSubtitle(
  title: string,
  kind: string,
  namespace: string | undefined,
  resource: K8sResource,
): string {
  const parts: string[] = [title];
  if (namespace) parts.push(`namespace ${namespace}`);

  if (kind === "deployments") {
    const dep = resource as Deployment;
    const ready = dep.status?.readyReplicas ?? 0;
    const desired = dep.spec?.replicas ?? 0;
    parts.push(`${ready}/${desired} replicas ready`);
  } else if (kind === "statefulsets") {
    const ss = resource as StatefulSet;
    const ready = ss.status?.readyReplicas ?? 0;
    const desired = ss.spec?.replicas ?? 0;
    parts.push(`${ready}/${desired} replicas ready`);
  } else if (kind === "daemonsets") {
    const ds = resource as DaemonSet;
    const ready = ds.status?.numberReady ?? 0;
    const desired = ds.status?.desiredNumberScheduled ?? 0;
    parts.push(`${ready}/${desired} pods ready`);
  } else if (kind === "pods") {
    const pod = resource as Pod;
    const created = pod.metadata.creationTimestamp;
    if (created) parts.push(`age ${age(created)}`);
  }

  return parts.join(" · ");
}

// Kinds that get a live metrics rail
const RAIL_KINDS = new Set([
  "deployments",
  "statefulsets",
  "daemonsets",
  "pods",
]);

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

  // Dirty state navigation guard
  const yamlContentRef = useRef("");
  const yamlDirty = useComputed(() =>
    yamlEditing.value && yamlEditContent.value !== yamlContentRef.current
  );
  useDirtyGuard(yamlDirty);

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

          if (
            resource.value && obj.metadata?.uid !== resource.value.metadata.uid
          ) {
            return;
          }

          switch (eventType) {
            case EVENT_MODIFIED:
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

  // Generate YAML from resource
  const yamlContent = useMemo(() => {
    if (!resource.value) return "";
    const obj = structuredClone(resource.value);
    if (!showManagedFields.value) {
      delete obj.metadata.managedFields;
    }
    try {
      return stringify(obj, { lineWidth: 0 });
    } catch {
      return JSON.stringify(obj, null, 2);
    }
  }, [resource.value, showManagedFields.value]);

  // Keep ref in sync for the dirty guard computed signal
  yamlContentRef.current = yamlContent;

  // Build back-to-list URL
  const listUrl = RESOURCE_DETAIL_PATHS[kind] ?? "/";

  // Force age() to use tick for reactivity
  void tick.value;

  // Compute pod selector for workload kinds
  const isWorkload = ["deployments", "statefulsets", "daemonsets"].includes(
    kind,
  );
  const podSelector = (() => {
    if (!isWorkload || !resource.value || !namespace) return "";
    const res = resource.value as Deployment | StatefulSet | DaemonSet;
    const matchLabels = res?.spec?.selector?.matchLabels ?? {};
    return Object.entries(matchLabels).map(([k, v]) => `${k}=${v}`).join(",");
  })();

  // ── tab definitions ────────────────────────────────────────────────────────

  const tabDefs: (DetailTab & { content: () => preact.JSX.Element | null })[] =
    [
      {
        id: "overview",
        label: "Overview",
        content: () => {
          if (loading.value) {
            return (
              <div style={{ padding: "32px" }}>
                <LoadingSpinner />
              </div>
            );
          }
          if (!resource.value) return null;
          const OverviewComponent = getOverviewComponent(kind);
          return (
            <div style={{ padding: "20px" }}>
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
              {/* Related pods for workload kinds — moved from SplitPane */}
              {isWorkload && namespace && podSelector && (
                <div style={{ marginTop: "20px" }}>
                  <RelatedPods
                    namespace={namespace}
                    labelSelector={podSelector}
                    parentName={name}
                  />
                </div>
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
            return (
              <div style={{ padding: "32px" }}>
                <LoadingSpinner />
              </div>
            );
          }

          return (
            <div style={{ padding: "20px" }}>
              {/* Updated externally banner */}
              {updated.value && (
                <div
                  style={{
                    display: "flex",
                    alignItems: "center",
                    gap: "12px",
                    borderRadius: "8px",
                    border:
                      "1px solid color-mix(in srgb, var(--info) 30%, transparent)",
                    background:
                      "color-mix(in srgb, var(--info) 10%, transparent)",
                    padding: "8px 16px",
                    fontSize: "13px",
                    color: "var(--info)",
                    marginBottom: "12px",
                  }}
                >
                  Resource was updated externally.
                  <button
                    type="button"
                    onClick={() => {
                      fetchResource();
                      yamlEditing.value = false;
                      yamlApplyError.value = null;
                      yamlApplySuccess.value = false;
                    }}
                    style={{
                      background: "none",
                      border: "none",
                      cursor: "pointer",
                      color: "inherit",
                      fontWeight: 500,
                      textDecoration: "underline",
                      fontSize: "13px",
                    }}
                  >
                    Refresh
                  </button>
                </div>
              )}

              {/* Apply success banner */}
              {yamlApplySuccess.value && (
                <div
                  style={{
                    display: "flex",
                    alignItems: "center",
                    gap: "12px",
                    borderRadius: "8px",
                    border:
                      "1px solid color-mix(in srgb, var(--success) 30%, transparent)",
                    background:
                      "color-mix(in srgb, var(--success) 10%, transparent)",
                    padding: "8px 16px",
                    fontSize: "13px",
                    color: "var(--success)",
                    marginBottom: "12px",
                  }}
                >
                  Changes applied successfully.
                  <button
                    type="button"
                    onClick={() => {
                      yamlApplySuccess.value = false;
                    }}
                    style={{
                      background: "none",
                      border: "none",
                      cursor: "pointer",
                      color: "inherit",
                      fontWeight: 500,
                      textDecoration: "underline",
                      fontSize: "13px",
                    }}
                  >
                    Dismiss
                  </button>
                </div>
              )}

              {/* Apply error banner */}
              {yamlApplyError.value && (
                <div
                  style={{
                    borderRadius: "8px",
                    border:
                      "1px solid color-mix(in srgb, var(--error) 30%, transparent)",
                    background:
                      "color-mix(in srgb, var(--error) 10%, transparent)",
                    padding: "12px 16px",
                    fontSize: "13px",
                    color: "var(--error)",
                    marginBottom: "12px",
                  }}
                >
                  <p style={{ fontWeight: 600, margin: "0 0 4px" }}>
                    Apply failed
                  </p>
                  <p style={{ margin: 0 }}>{yamlApplyError.value}</p>
                </div>
              )}

              {/* YAML toolbar */}
              <div
                style={{
                  display: "flex",
                  alignItems: "center",
                  justifyContent: "space-between",
                  marginBottom: "10px",
                }}
              >
                <label
                  style={{
                    display: "flex",
                    alignItems: "center",
                    gap: "8px",
                    fontSize: "13px",
                    color: "var(--text-muted)",
                    cursor: "pointer",
                  }}
                >
                  <input
                    type="checkbox"
                    checked={showManagedFields.value}
                    onChange={(e) => {
                      showManagedFields.value =
                        (e.target as HTMLInputElement).checked;
                    }}
                    disabled={yamlEditing.value}
                  />
                  Show managed fields
                </label>
                <div style={{ display: "flex", gap: "8px" }}>
                  {!yamlEditing.value
                    ? (
                      <>
                        <GhostButton
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
                        >
                          Edit
                        </GhostButton>
                        <GhostButton
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
                        >
                          Export
                        </GhostButton>
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
                          style={primaryButtonStyle(
                            yamlApplying.value ||
                              yamlEditContent.value === yamlContent,
                          )}
                        >
                          {yamlApplying.value ? "Applying…" : "Apply"}
                        </button>
                        <GhostButton
                          onClick={() => {
                            yamlEditing.value = false;
                            yamlEditContent.value = "";
                            yamlApplyError.value = null;
                          }}
                          disabled={yamlApplying.value}
                        >
                          Discard
                        </GhostButton>
                      </>
                    )}
                </div>
              </div>

              {/* CodeMirror YAML editor */}
              <div
                style={{
                  borderRadius: "9px",
                  border: "1px solid var(--border-primary)",
                  overflow: "hidden",
                }}
              >
                <CodeMirrorEditor
                  value={yamlEditing.value
                    ? yamlEditContent.value
                    : yamlContent}
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
            return (
              <div style={{ padding: "32px" }}>
                <LoadingSpinner />
              </div>
            );
          }
          if (eventsError.value) {
            return (
              <div style={{ padding: "20px" }}>
                <ErrorBanner message={eventsError.value} />
              </div>
            );
          }
          if (events.value.length === 0) {
            return (
              <div
                style={{
                  padding: "48px",
                  textAlign: "center",
                  fontSize: "13px",
                  color: "var(--text-muted)",
                }}
              >
                No events found for this resource
              </div>
            );
          }
          return (
            <div style={{ padding: "20px" }}>
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

  // Add Logs and Terminal tabs for pods
  if (kind === "pods" && namespace && IS_BROWSER) {
    tabDefs.push({
      id: "logs",
      label: "Logs",
      content: () => {
        const res = resource.value as Pod;
        const containers: string[] = res?.spec?.containers?.map((c) =>
          c.name
        ) ?? [];
        const initContainers: string[] = res?.spec?.initContainers?.map((c) =>
          c.name
        ) ?? [];
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
        const res = resource.value as Pod;
        const containers: string[] = res?.spec?.containers?.map((c) =>
          c.name
        ) ?? [];
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

  // Add "Loki Logs" tab for workloads and services
  if (
    namespace &&
    [
      "deployments",
      "statefulsets",
      "daemonsets",
      "services",
      "jobs",
      "cronjobs",
    ].includes(kind)
  ) {
    tabDefs.push({
      id: "loki-logs",
      label: "Loki Logs",
      content: () => {
        const logsUrl = `/observability/logs?namespace=${
          encodeURIComponent(namespace)
        }&kind=${encodeURIComponent(kind)}&name=${encodeURIComponent(name)}`;
        return (
          <div
            style={{
              padding: "32px",
              textAlign: "center",
            }}
          >
            <p
              style={{
                fontSize: "13px",
                color: "var(--text-muted)",
                marginBottom: "16px",
              }}
            >
              View aggregated logs for this resource in the Log Explorer.
            </p>
            <a
              href={logsUrl}
              style={{
                display: "inline-flex",
                alignItems: "center",
                gap: "8px",
                padding: "8px 16px",
                borderRadius: "9px",
                background: "var(--accent)",
                color: "var(--bg-base)",
                fontSize: "13px",
                fontWeight: 600,
                textDecoration: "none",
              }}
            >
              Open Log Explorer
            </a>
          </div>
        );
      },
    });
  }

  // ── derived display values ─────────────────────────────────────────────────

  const status = resource.value
    ? deriveStatus(kind, resource.value)
    : undefined;

  const subtitle = resource.value
    ? deriveSubtitle(title, kind, namespace, resource.value)
    : namespace
    ? `${title} · namespace ${namespace}`
    : title;

  // Action buttons for DetailShell
  const actionButtons = resource.value && actions.value.length > 0
    ? (
      <>
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
                padding: "6px 12px",
                borderRadius: "9px",
                fontSize: "12px",
                fontWeight: 600,
                fontFamily: "inherit",
                border: isDanger
                  ? "1px solid color-mix(in srgb, var(--error) 40%, transparent)"
                  : "1px solid var(--border-primary)",
                background: "transparent",
                color: isDanger ? "var(--error)" : "var(--text-muted)",
                cursor: actionLoading.value ? "not-allowed" : "pointer",
                display: "flex",
                alignItems: "center",
                gap: "5px",
                opacity: actionLoading.value ? 0.5 : 1,
                transition: "background 0.15s",
              }}
              onMouseOver={(e) => {
                if (!actionLoading.value) {
                  (e.currentTarget as HTMLElement).style.background = isDanger
                    ? "color-mix(in srgb, var(--error) 12%, transparent)"
                    : "var(--bg-elevated)";
                }
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
        {/* Investigate link */}
        {namespace && (
          <a
            href={`/observability/investigate?namespace=${namespace}&kind=${
              RESOURCE_API_KINDS[kind] ?? kind
            }&name=${name}`}
            style={{
              padding: "6px 12px",
              borderRadius: "9px",
              fontSize: "12px",
              fontWeight: 600,
              fontFamily: "inherit",
              border: "1px solid var(--border-primary)",
              background: "transparent",
              color: "var(--text-muted)",
              display: "inline-flex",
              alignItems: "center",
              gap: "5px",
              textDecoration: "none",
              transition: "background 0.15s, color 0.15s",
            }}
            onMouseOver={(e) => {
              (e.currentTarget as HTMLElement).style.background =
                "var(--bg-elevated)";
              (e.currentTarget as HTMLElement).style.color = "var(--accent)";
            }}
            onMouseOut={(e) => {
              (e.currentTarget as HTMLElement).style.background = "transparent";
              (e.currentTarget as HTMLElement).style.color =
                "var(--text-muted)";
            }}
          >
            Investigate
          </a>
        )}
      </>
    )
    : undefined;

  // Metrics rail — only for rail-eligible kinds with a namespace
  const metricsRail = (IS_BROWSER && RAIL_KINDS.has(kind) && namespace)
    ? <MetricsRail kind={kind} name={name} namespace={namespace} />
    : undefined;

  // Active tab content
  const activeTabDef = tabDefs.find((t) => t.id === activeTab.value);

  return (
    <>
      {/* Deleted banner */}
      {deleted.value && (
        <div
          style={{
            borderRadius: "9px",
            border:
              "1px solid color-mix(in srgb, var(--warning) 30%, transparent)",
            background: "color-mix(in srgb, var(--warning) 10%, transparent)",
            padding: "12px 16px",
            fontSize: "13px",
            color: "var(--warning)",
            marginBottom: "12px",
          }}
        >
          This {title.toLowerCase()} was deleted.{" "}
          <a
            href={listUrl}
            style={{
              fontWeight: 600,
              color: "inherit",
              textDecoration: "underline",
            }}
          >
            Back to {pluralize(title.toLowerCase())} list
          </a>
        </div>
      )}

      <DetailShell
        icon={<ResourceIcon kind={kind} size={22} />}
        title={name}
        subtitle={subtitle}
        status={status}
        actions={actionButtons}
        tabs={tabDefs}
        active={activeTab.value}
        onTab={handleTabChange}
        rail={metricsRail}
      >
        {/* Error state */}
        {error.value && !resource.value
          ? (
            <div style={{ padding: "20px" }}>
              <ErrorBanner message={error.value} />
            </div>
          )
          : (
            <div
              style={{
                background: "var(--bg-surface)",
                borderRadius: "12px",
                border: "1px solid var(--border-subtle)",
                overflow: "hidden",
              }}
            >
              {activeTabDef ? activeTabDef.content() : null}
            </div>
          )}
      </DetailShell>

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
            <div>
              <p style={{ fontSize: "13px", marginBottom: "12px" }}>
                Set the number of replicas:
              </p>
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
                style={{
                  width: "100%",
                  borderRadius: "9px",
                  border: "1px solid var(--border-primary)",
                  background: "var(--bg-surface)",
                  padding: "8px 12px",
                  fontSize: "13px",
                  color: "var(--text-primary)",
                }}
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
    </>
  );
}

// ── helpers ───────────────────────────────────────────────────────────────────

function primaryButtonStyle(disabled: boolean): preact.JSX.CSSProperties {
  return {
    padding: "6px 14px",
    borderRadius: "9px",
    fontSize: "12px",
    fontWeight: 600,
    fontFamily: "inherit",
    border: "none",
    background: "var(--accent)",
    color: "var(--bg-base)",
    cursor: disabled ? "not-allowed" : "pointer",
    opacity: disabled ? 0.5 : 1,
  };
}

function GhostButton(
  {
    children,
    onClick,
    disabled,
    title,
  }: {
    children: preact.ComponentChildren;
    onClick?: () => void;
    disabled?: boolean;
    title?: string;
  },
) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      title={title}
      style={{
        padding: "6px 12px",
        borderRadius: "9px",
        fontSize: "12px",
        fontWeight: 600,
        fontFamily: "inherit",
        border: "1px solid var(--border-primary)",
        background: "transparent",
        color: "var(--text-muted)",
        cursor: disabled ? "not-allowed" : "pointer",
        opacity: disabled ? 0.5 : 1,
      }}
    >
      {children}
    </button>
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
    <div style={{ overflowX: "auto" }}>
      <table
        style={{ width: "100%", fontSize: "13px", borderCollapse: "collapse" }}
      >
        <thead>
          <tr
            style={{
              borderBottom: "1px solid var(--border-primary)",
            }}
          >
            {["Type", "Reason", "Message", "Count", "Last Seen"].map((h) => (
              <th
                key={h}
                style={{
                  padding: "10px 16px",
                  textAlign: "left",
                  fontSize: "11px",
                  fontWeight: 600,
                  textTransform: "uppercase",
                  letterSpacing: "0.07em",
                  color: "var(--text-muted)",
                }}
              >
                {h}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {events.map((e) => (
            <tr
              key={e.metadata.uid}
              style={{ borderBottom: "1px solid var(--border-subtle)" }}
            >
              <td style={{ padding: "10px 16px" }}>
                <span
                  style={{
                    display: "inline-flex",
                    alignItems: "center",
                    padding: "2px 8px",
                    borderRadius: "6px",
                    fontSize: "11px",
                    fontWeight: 600,
                    background: e.type === "Warning"
                      ? "color-mix(in srgb, var(--warning) 14%, transparent)"
                      : "var(--bg-elevated)",
                    color: e.type === "Warning"
                      ? "var(--warning)"
                      : "var(--text-muted)",
                  }}
                >
                  {e.type ?? "Normal"}
                </span>
              </td>
              <td
                style={{
                  padding: "10px 16px",
                  color: "var(--text-muted)",
                  fontSize: "13px",
                }}
              >
                {e.reason ?? "-"}
              </td>
              <td
                style={{
                  padding: "10px 16px",
                  color: "var(--text-muted)",
                  fontSize: "13px",
                  maxWidth: "400px",
                  overflow: "hidden",
                  textOverflow: "ellipsis",
                  whiteSpace: "nowrap",
                }}
              >
                {e.message ?? "-"}
              </td>
              <td
                style={{
                  padding: "10px 16px",
                  color: "var(--text-muted)",
                  fontSize: "13px",
                }}
              >
                {e.count ?? 1}
              </td>
              <td
                style={{
                  padding: "10px 16px",
                  color: "var(--text-muted)",
                  fontSize: "13px",
                }}
              >
                {e.lastTimestamp ? age(e.lastTimestamp) : "-"}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
