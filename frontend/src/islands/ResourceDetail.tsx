import {
  type CSSProperties,
  type ReactElement,
  type ReactNode,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { stringify } from "yaml";
import DetailShell, { type DetailTab } from "@/components/k8s/DetailShell.tsx";
import { MetadataSection } from "@/components/k8s/detail/MetadataSection.tsx";
import { ResourceIcon } from "@/components/k8s/ResourceIcon.tsx";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog.tsx";
import { ErrorBanner } from "@/components/ui/ErrorBanner.tsx";
import type { Tone } from "@/components/ui/glass/StatusBadge.tsx";
import { LoadingSpinner } from "@/components/ui/LoadingSpinner.tsx";
import {
  type ActionId,
  executeAction,
  getActionMeta,
  getVisibleActions,
} from "@/lib/action-handlers.ts";
import { ApiError, apiGet, apiPostRaw } from "@/lib/api.ts";
import { useAuth } from "@/lib/auth.ts";
import {
  INVESTIGATE_KINDS,
  PINNABLE_KINDS,
  RESOURCE_API_KINDS,
  RESOURCE_DETAIL_PATHS,
} from "@/lib/constants.ts";
import { age } from "@/lib/format.ts";
import { useDirtyGuard } from "@/lib/hooks/use-dirty-guard.ts";
import type {
  DaemonSet,
  Deployment,
  K8sEvent,
  K8sResource,
  Pod,
  StatefulSet,
} from "@/lib/k8s-types.ts";
import type { ApplyResponse } from "@/lib/yaml-apply.ts";
import { getOverviewComponent } from "@/src/components/k8s/detail/index.tsx";
import { PinToggle } from "@/src/components/k8s/PinToggle.tsx";
import { CodeMirrorEditor } from "@/src/components/ui/CodeMirrorEditor.tsx";
import LogViewer from "@/src/islands/LogViewer.tsx";
import MetricsRail from "@/src/islands/MetricsRail.tsx";
import PerformancePanel, {
  hasPerformanceMetrics,
} from "@/src/islands/PerformancePanel.tsx";
import PodTerminal from "@/src/islands/PodTerminal.tsx";
import RelatedPods from "@/src/islands/RelatedPods.tsx";
import RoleBindingsList from "@/src/islands/RoleBindingsList.tsx";
import { selectedNamespace } from "@/src/lib/namespace.ts";
import { showToast } from "@/src/lib/toast-store.ts";
import { useIsClient } from "@/src/lib/use-is-client.ts";
import { useLatest } from "@/src/lib/use-latest.ts";
import { useSignalValue } from "@/src/lib/use-signal-value.ts";
import {
  EVENT_DELETED,
  EVENT_MODIFIED,
  EVENT_RESYNC,
  subscribe,
} from "@/src/lib/ws.ts";

interface ResourceDetailProps {
  kind: string;
  name: string;
  namespace?: string;
  clusterScoped?: boolean;
  title: string;
}

function pluralize(s: string): string {
  if (s.endsWith("y") && !s.endsWith("ey")) return `${s.slice(0, -1)}ies`;
  if (
    s.endsWith("s") ||
    s.endsWith("x") ||
    s.endsWith("ch") ||
    s.endsWith("sh")
  )
    return `${s}es`;
  return `${s}s`;
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
      const allReady =
        pod.status?.containerStatuses?.every((c) => c.ready) ?? false;
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

/**
 * The error to show for an apply whose documents the server rejected, or
 * null when every document applied.
 */
function applyFailureMessage(res: ApplyResponse | undefined): string | null {
  if (!res?.summary || res.summary.failed === 0) return null;
  const errors = (res.results ?? [])
    .filter((r) => r.action === "failed")
    .map((r) => r.error ?? `${r.kind} ${r.name} was not applied`);
  return errors.length > 0 ? errors.join("; ") : "Apply failed";
}

/** Renders a resource as the YAML the detail page shows and edits. */
function resourceToYaml(
  resource: K8sResource,
  includeManagedFields: boolean,
): string {
  const obj = structuredClone(resource);
  if (!includeManagedFields) {
    delete obj.metadata.managedFields;
  }
  try {
    return stringify(obj, { lineWidth: 0 });
  } catch {
    return JSON.stringify(obj, null, 2);
  }
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
  const isClient = useIsClient();
  const [resource, setResource] = useState<K8sResource | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [deleted, setDeleted] = useState(false);
  const [updated, setUpdated] = useState(false);
  const [activeTab, setActiveTab] = useState("overview");

  // Events tab state
  const [events, setEvents] = useState<K8sEvent[]>([]);
  const [eventsLoading, setEventsLoading] = useState(false);
  const [eventsError, setEventsError] = useState<string | null>(null);
  const eventsFetched = useRef(false);

  // YAML options
  const [showManagedFields, setShowManagedFields] = useState(false);

  // YAML edit state
  const [yamlEditing, setYamlEditing] = useState(false);
  const [yamlEditContent, setYamlEditContent] = useState("");
  const [yamlApplying, setYamlApplying] = useState(false);
  const yamlApplyingRef = useRef(false);
  const [yamlApplyError, setYamlApplyError] = useState<string | null>(null);
  const [yamlApplySuccess, setYamlApplySuccess] = useState(false);
  const isSecret = kind === "secrets";

  // Action buttons state
  const { rbac: rbacSignal } = useAuth();
  const rbac = useSignalValue(rbacSignal);
  const [actionLoading, setActionLoading] = useState(false);
  const actionLoadingRef = useRef(false);
  const [confirmAction, setConfirmAction] = useState<{
    actionId: ActionId;
    params?: Record<string, unknown>;
  } | null>(null);
  const [scaleTarget, setScaleTarget] = useState(false);
  const [scaleValue, setScaleValue] = useState(1);

  const actions = useMemo(
    () => getVisibleActions(kind, namespace ?? "", rbac),
    [kind, namespace, rbac],
  );

  const runAction = async (
    actionId: ActionId,
    params?: Record<string, unknown>,
  ) => {
    if (actionLoadingRef.current) return;
    actionLoadingRef.current = true;
    setActionLoading(true);
    try {
      const message = await executeAction(
        actionId,
        kind,
        namespace ?? "",
        name,
        params,
      );
      showToast(message, "success");
      setConfirmAction(null);
      setScaleTarget(false);
    } catch (err) {
      const msg = err instanceof Error ? err.message : "Action failed";
      showToast(msg, "error");
    } finally {
      actionLoadingRef.current = false;
      setActionLoading(false);
    }
  };

  const handleAction = (actionId: ActionId) => {
    if (actionLoadingRef.current || !resource) return;
    const meta = getActionMeta(actionId, resource, kind);

    if (actionId === "scale") {
      const spec = resource.spec as { replicas?: number } | undefined;
      setScaleValue(spec?.replicas ?? 1);
      setScaleTarget(true);
      return;
    }

    if (meta.confirm) {
      let params: Record<string, unknown> | undefined;
      if (actionId === "suspend") {
        const spec = resource.spec as { suspend?: boolean } | undefined;
        params = { suspend: !spec?.suspend };
      }
      setConfirmAction({ actionId, params });
      return;
    }

    runAction(actionId);
  };

  // Generate YAML from resource
  const yamlContent = useMemo(
    () => (resource ? resourceToYaml(resource, showManagedFields) : ""),
    [resource, showManagedFields],
  );

  useDirtyGuard(yamlEditing && yamlEditContent !== yamlContent);

  // Periodic tick to refresh age displays (every 30s)
  const [, setTick] = useState(0);
  useEffect(() => {
    const id = setInterval(() => {
      setTick((t) => t + 1);
    }, 30_000);
    return () => clearInterval(id);
  }, []);

  // Which tabs exist for this resource. Logs and Terminal open sockets, so
  // they join the list below only once the page is on the client.
  const hasMetricsTab = hasPerformanceMetrics(kind);
  const hasPodTabs = kind === "pods" && !!namespace;
  const hasLokiTab =
    !!namespace &&
    [
      "deployments",
      "statefulsets",
      "daemonsets",
      "services",
      "jobs",
      "cronjobs",
    ].includes(kind);

  const tabAvailability = useLatest({ hasMetricsTab, hasPodTabs, hasLokiTab });
  useEffect(() => {
    const hash = globalThis.location.hash.replace("#", "");
    const { hasMetricsTab, hasPodTabs, hasLokiTab } = tabAvailability.current;
    const known = [
      "overview",
      "yaml",
      "events",
      ...(hasMetricsTab ? ["metrics"] : []),
      ...(hasPodTabs ? ["logs", "terminal"] : []),
      ...(hasLokiTab ? ["loki-logs"] : []),
    ];
    if (hash && known.includes(hash)) {
      setActiveTab(hash);
    }
  }, []);

  // Set document title
  useEffect(() => {
    document.title = `${name} - ${title} - k8sCenter`;
    return () => {
      document.title = "k8sCenter";
    };
  }, [name, title]);

  // Navigate to list page when namespace selector changes
  useEffect(() => {
    if (clusterScoped) return;
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
    setLoading(true);
    setError(null);
    try {
      const path = namespace
        ? `/v1/resources/${kind}/${namespace}/${name}`
        : `/v1/resources/${kind}/${name}`;
      const res = await apiGet<K8sResource>(path);
      setResource(res.data);
      setUpdated(false);
      eventsFetched.current = false;
    } catch (err) {
      // Branch on the typed status, not the message text. A substring match
      // for "404" also fires on a 403 whose message happens to contain it,
      // and "gone" versus "not allowed to see" are different facts to tell a
      // user — the pin control's states depend on that distinction too.
      const status = err instanceof ApiError ? err.status : undefined;
      if (status === 404) {
        setError(`${title} "${name}" not found`);
      } else if (status === 403) {
        setError(
          `You don't have permission to view this ${title.toLowerCase()}`,
        );
      } else {
        setError(
          err instanceof Error ? err.message : "Failed to load resource",
        );
      }
    } finally {
      setLoading(false);
    }
  }, [kind, name, namespace, title]);

  // The socket callback outlives the render that registered it.
  const resourceRef = useLatest(resource);
  const activeTabRef = useLatest(activeTab);
  const fetchResourceRef = useLatest(fetchResource);

  // Subscribe to WS and fetch resource
  useEffect(() => {
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

          const current = resourceRef.current;
          if (current && obj.metadata?.uid !== current.metadata.uid) {
            return;
          }

          switch (eventType) {
            case EVENT_MODIFIED:
              if (activeTabRef.current === "yaml") {
                setUpdated(true);
              } else {
                setResource(obj);
              }
              break;
            case EVENT_DELETED:
              setDeleted(true);
              break;
            case EVENT_RESYNC:
              fetchResourceRef.current();
              break;
          }
        },
      );
    }

    fetchResourceRef.current();

    return () => {
      unsubscribe?.();
    };
  }, [kind, name, namespace]);

  // Fetch events when Events tab is first activated
  const fetchEvents = useCallback(async () => {
    if (eventsFetched.current) return;
    eventsFetched.current = true;
    setEventsLoading(true);
    setEventsError(null);
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
      setEvents(Array.isArray(res.data) ? res.data : []);
    } catch (err) {
      setEventsError(
        err instanceof Error ? err.message : "Failed to load events",
      );
    } finally {
      setEventsLoading(false);
    }
  }, [kind, name, namespace, title]);

  // Tab change handler
  const handleTabChange = useCallback(
    (tabId: string) => {
      setActiveTab(tabId);
      history.replaceState(null, "", `#${tabId}`);
      if (tabId === "events") {
        fetchEvents();
      }
    },
    [fetchEvents],
  );

  // Build back-to-list URL
  const listUrl = RESOURCE_DETAIL_PATHS[kind] ?? "/";

  // Compute pod selector for workload kinds
  const isWorkload = ["deployments", "statefulsets", "daemonsets"].includes(
    kind,
  );
  const podSelector = (() => {
    if (!isWorkload || !resource || !namespace) return "";
    const res = resource as Deployment | StatefulSet | DaemonSet;
    const matchLabels = res?.spec?.selector?.matchLabels ?? {};
    return Object.entries(matchLabels)
      .map(([k, v]) => `${k}=${v}`)
      .join(",");
  })();

  // ── tab definitions ────────────────────────────────────────────────────────

  const tabDefs: (DetailTab & { content: () => ReactElement | null })[] = [
    {
      id: "overview",
      label: "Overview",
      content: () => {
        if (loading) {
          return (
            <div style={{ padding: "32px" }}>
              <LoadingSpinner />
            </div>
          );
        }
        if (!resource) return null;
        const OverviewComponent = getOverviewComponent(kind);
        return (
          <div style={{ padding: "20px" }}>
            {kind !== "deployments" && <MetadataSection resource={resource} />}
            <OverviewComponent resource={resource} />
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
        if (loading || !resource) {
          return (
            <div style={{ padding: "32px" }}>
              <LoadingSpinner />
            </div>
          );
        }

        return (
          <div style={{ padding: "20px" }}>
            {/* Updated externally banner */}
            {updated && (
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
                    setYamlEditing(false);
                    setYamlApplyError(null);
                    setYamlApplySuccess(false);
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
            {yamlApplySuccess && (
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
                    setYamlApplySuccess(false);
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
            {yamlApplyError && (
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
                <p style={{ margin: 0 }}>{yamlApplyError}</p>
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
                  checked={showManagedFields}
                  onChange={(e) => {
                    setShowManagedFields(e.currentTarget.checked);
                  }}
                  disabled={yamlEditing}
                />
                Show managed fields
              </label>
              <div style={{ display: "flex", gap: "8px" }}>
                {!yamlEditing ? (
                  <>
                    <GhostButton
                      onClick={() => {
                        // An apply that carries managedFields is rejected
                        // by the API server, so editing always starts from
                        // the manifest without them.
                        setShowManagedFields(false);
                        setYamlEditContent(
                          resource ? resourceToYaml(resource, false) : "",
                        );
                        setYamlEditing(true);
                        setYamlApplyError(null);
                        setYamlApplySuccess(false);
                      }}
                      disabled={isSecret}
                      title={
                        isSecret
                          ? "Secrets cannot be edited via YAML"
                          : "Edit YAML"
                      }
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
                          setYamlApplyError(
                            err instanceof Error
                              ? err.message
                              : "Export failed",
                          );
                        }
                      }}
                      disabled={isSecret}
                      title={
                        isSecret
                          ? "Secrets cannot be exported (values are masked)"
                          : "Export clean YAML"
                      }
                    >
                      Export
                    </GhostButton>
                  </>
                ) : (
                  <>
                    <button
                      type="button"
                      onClick={async () => {
                        if (yamlApplyingRef.current) return;
                        yamlApplyingRef.current = true;
                        setYamlApplying(true);
                        setYamlApplyError(null);
                        setYamlApplySuccess(false);
                        try {
                          const res = await apiPostRaw<ApplyResponse>(
                            "/v1/yaml/apply",
                            yamlEditContent,
                          );
                          // /yaml/apply answers 200 even when the
                          // server-side apply rejects the document (a field
                          // ownership conflict, a stale resourceVersion, an
                          // invalid field). Keep the edit and show why.
                          const failure = applyFailureMessage(res.data);
                          if (failure) {
                            setYamlApplyError(failure);
                            return;
                          }
                          setYamlApplySuccess(true);
                          setYamlEditing(false);
                          await fetchResource();
                        } catch (err) {
                          setYamlApplyError(
                            err instanceof Error ? err.message : "Apply failed",
                          );
                        } finally {
                          yamlApplyingRef.current = false;
                          setYamlApplying(false);
                        }
                      }}
                      disabled={yamlApplying || yamlEditContent === yamlContent}
                      style={primaryButtonStyle(
                        yamlApplying || yamlEditContent === yamlContent,
                      )}
                    >
                      {yamlApplying ? "Applying…" : "Apply"}
                    </button>
                    <GhostButton
                      onClick={() => {
                        setYamlEditing(false);
                        setYamlEditContent("");
                        setYamlApplyError(null);
                      }}
                      disabled={yamlApplying}
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
                value={yamlEditing ? yamlEditContent : yamlContent}
                onChange={yamlEditing ? setYamlEditContent : undefined}
                readOnly={!yamlEditing}
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
        if (eventsLoading) {
          return (
            <div style={{ padding: "32px" }}>
              <LoadingSpinner />
            </div>
          );
        }
        if (eventsError) {
          return (
            <div style={{ padding: "20px" }}>
              <ErrorBanner message={eventsError} />
            </div>
          );
        }
        if (events.length === 0) {
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
            <EventsTable events={events} />
          </div>
        );
      },
    },
  ];

  // Only kinds with metric queries get a Metrics tab; for the rest
  // (ConfigMap, Secret, Endpoints, …) it could only say "No Metrics Available".
  if (hasMetricsTab) {
    tabDefs.push({
      id: "metrics",
      label: "Metrics",
      content: () => (
        <PerformancePanel kind={kind} name={name} namespace={namespace} />
      ),
    });
  }

  // Add Logs and Terminal tabs for pods
  if (hasPodTabs && isClient) {
    tabDefs.push({
      id: "logs",
      label: "Logs",
      content: () => {
        const res = resource as Pod;
        const containers: string[] =
          res?.spec?.containers?.map((c) => c.name) ?? [];
        const initContainers: string[] =
          res?.spec?.initContainers?.map((c) => c.name) ?? [];
        // A #logs deep link opens this tab before the pod has loaded. The
        // viewer streams the containers it mounts with, so wait for the real
        // names rather than open a stream for a container that is not there.
        if (containers.length === 0) return <LoadingSpinner />;
        return (
          <LogViewer
            namespace={namespace}
            pod={name}
            containers={containers}
            initContainers={initContainers}
          />
        );
      },
    });
    tabDefs.push({
      id: "terminal",
      label: "Terminal",
      content: () => {
        const res = resource as Pod;
        const containers: string[] =
          res?.spec?.containers?.map((c) => c.name) ?? [];
        if (containers.length === 0) return <LoadingSpinner />;
        return (
          <PodTerminal
            namespace={namespace}
            name={name}
            containers={containers}
          />
        );
      },
    });
  }

  // Add "Loki Logs" tab for workloads and services
  if (hasLokiTab && namespace) {
    tabDefs.push({
      id: "loki-logs",
      label: "Loki Logs",
      content: () => {
        const logsUrl = `/observability/logs?namespace=${encodeURIComponent(
          namespace,
        )}&kind=${encodeURIComponent(kind)}&name=${encodeURIComponent(name)}`;
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

  const status = resource ? deriveStatus(kind, resource) : undefined;

  const subtitle = resource
    ? deriveSubtitle(title, kind, namespace, resource)
    : namespace
      ? `${title} · namespace ${namespace}`
      : title;

  // Action buttons for DetailShell.
  //
  // Gated on the resource alone. Gating the whole block on
  // actions.value.length > 0 as well would hide the pin control and the
  // Investigate link from anyone whose RBAC grants no write actions — the
  // read-only users for whom a pin is most of the value. The RBAC gate stays
  // where it belongs, on the action list itself, which renders nothing when
  // empty.
  const actionButtons = resource ? (
    <>
      {actions.map((actionId) => {
        const meta = getActionMeta(actionId, resource);
        const isDanger = !!meta.danger;
        return (
          <button
            key={actionId}
            type="button"
            onClick={() => handleAction(actionId)}
            disabled={actionLoading}
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
              cursor: actionLoading ? "not-allowed" : "pointer",
              display: "flex",
              alignItems: "center",
              gap: "5px",
              opacity: actionLoading ? 0.5 : 1,
              transition: "background 0.15s",
            }}
            onMouseOver={(e) => {
              if (!actionLoading) {
                (e.currentTarget as HTMLElement).style.background = isDanger
                  ? "color-mix(in srgb, var(--error) 12%, transparent)"
                  : "var(--bg-elevated)";
              }
            }}
            onMouseOut={(e) => {
              (e.currentTarget as HTMLElement).style.background = "transparent";
            }}
          >
            <ActionIcon actionId={actionId} />
            {meta.label}
          </button>
        );
      })}
      {/* Investigate link — only for kinds the diagnostics backend
            supports. It answers 400 for anything outside INVESTIGATE_KINDS,
            so offering the link elsewhere just routes the user to an error. */}
      {namespace && INVESTIGATE_KINDS.has(RESOURCE_API_KINDS[kind] ?? kind) && (
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
            (e.currentTarget as HTMLElement).style.color = "var(--text-muted)";
          }}
        >
          Investigate
        </a>
      )}
      {/* Pin control — only for kinds the preferences API can store. It
            rejects anything without a registered adapter, so elsewhere this
            would be a button that can only ever fail. */}
      {PINNABLE_KINDS.has(kind) && (
        <PinToggle
          resourceKind={kind}
          displayKind={RESOURCE_API_KINDS[kind] ?? title}
          namespace={namespace ?? ""}
          name={name}
          uid={resource?.metadata.uid}
          deleted={deleted}
        />
      )}
    </>
  ) : undefined;

  // Metrics rail — only for rail-eligible kinds with a namespace.
  //
  // Never gate this on IS_BROWSER. The rail decides whether DetailShell
  // renders its two-column grid, so the server and the first browser render
  // must agree on it: hydration adopts the server's elements without
  // re-applying their props, and a grid the server did not emit strands every
  // element below it one level off (the loaded tab ends up inside the loading
  // spinner's flex box). MetricsRail is SSR-safe on its own: it renders
  // nothing on the server.
  const metricsRail =
    RAIL_KINDS.has(kind) && namespace ? (
      <MetricsRail kind={kind} name={name} namespace={namespace} />
    ) : undefined;

  // Active tab content
  const activeTabDef = tabDefs.find((t) => t.id === activeTab);

  return (
    <>
      {/* Deleted banner */}
      {deleted && (
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
        active={activeTab}
        onTab={handleTabChange}
        rail={metricsRail}
      >
        {/* Error state */}
        {error && !resource ? (
          <div style={{ padding: "20px" }} data-testid="detail-error">
            <ErrorBanner message={error} />
          </div>
        ) : (
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
      {confirmAction &&
        resource &&
        (() => {
          const meta = getActionMeta(confirmAction.actionId, resource, kind);
          const isDestructive = meta.confirm === "destructive";
          return (
            <ConfirmDialog
              title={`${meta.label} ${name}`}
              message={meta.confirmMessage}
              confirmLabel={meta.label}
              danger={meta.danger}
              typeToConfirm={isDestructive ? name : undefined}
              onConfirm={() =>
                runAction(confirmAction.actionId, confirmAction.params)
              }
              onCancel={() => {
                setConfirmAction(null);
              }}
            />
          );
        })()}

      {/* Scale dialog */}
      {scaleTarget && (
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
                value={Number.isNaN(scaleValue) ? "" : scaleValue}
                onChange={(e) => {
                  setScaleValue(parseInt(e.currentTarget.value, 10));
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
          onConfirm={() => runAction("scale", { replicas: scaleValue })}
          onCancel={() => {
            setScaleTarget(false);
          }}
        />
      )}
    </>
  );
}

// ── helpers ───────────────────────────────────────────────────────────────────

function primaryButtonStyle(disabled: boolean): CSSProperties {
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

function GhostButton({
  children,
  onClick,
  disabled,
  title,
}: {
  children: ReactNode;
  onClick?: () => void;
  disabled?: boolean;
  title?: string;
}) {
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
    strokeWidth: "1.5",
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
                    background:
                      e.type === "Warning"
                        ? "color-mix(in srgb, var(--warning) 14%, transparent)"
                        : "var(--bg-elevated)",
                    color:
                      e.type === "Warning"
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
