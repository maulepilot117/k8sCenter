import { useComputed, useSignal } from "@preact/signals";
import { useCallback, useEffect, useRef } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiGet } from "@/lib/api.ts";
import { selectedNamespace } from "@/lib/namespace.ts";
import {
  EVENT_ADDED,
  EVENT_DELETED,
  EVENT_MODIFIED,
  EVENT_RESYNC,
  subscribe,
} from "@/lib/ws.ts";
import { RESOURCE_COLUMNS } from "@/lib/resource-columns.ts";
import {
  CLUSTER_SCOPED_KINDS,
  RESOURCE_DETAIL_PATHS,
} from "@/lib/constants.ts";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog.tsx";
import ResourceTable, {
  type Column as UIColumn,
  type Row as UIRow,
} from "@/components/ui/ResourceTable.tsx";
import { ScaleDialog } from "@/components/ui/ScaleDialog.tsx";
import { SearchBar } from "@/components/ui/SearchBar.tsx";
import { showToast } from "@/islands/ToastProvider.tsx";
import type { Deployment, K8sResource, Pod } from "@/lib/k8s-types.ts";
import type { ActionId } from "@/lib/action-handlers.ts";
import {
  executeAction,
  getActionMeta,
  getVisibleActions,
} from "@/lib/action-handlers.ts";
import { useAuth } from "@/lib/auth.ts";
import { ErrorBanner } from "@/components/ui/ErrorBanner.tsx";
import { canPerform as canPerformCheck } from "@/lib/permissions.ts";

interface ResourceTableIslandProps {
  /** API kind string matching backend route, e.g."pods","deployments" */
  kind: string;
  /** Display title for the page header */
  title: string;
  /** Whether this resource is cluster-scoped (no namespace filtering) */
  clusterScoped?: boolean;
  /** Whether to subscribe to WebSocket events (false for secrets) */
  enableWS?: boolean;
  /** URL for "Create" button (if provided, shows a Create button in header) */
  createHref?: string;
  /** Hide the built-in title/create header (when wrapped by a parent dashboard) */
  hideHeader?: boolean;
}

const PAGE_SIZE = 100;

export default function ResourceTableIsland({
  kind,
  title,
  clusterScoped = false,
  enableWS = true,
  hideHeader = false,
  createHref,
}: ResourceTableIslandProps) {
  const items = useSignal<K8sResource[]>([]);
  const loading = useSignal(true);
  const error = useSignal<string | null>(null);
  const search = useSignal("");
  const statusFilter = useSignal("all");
  const sortKey = useSignal("name");
  const sortDir = useSignal<"asc" | "desc">("asc");
  const continueToken = useSignal<string | null>(null);
  const totalCount = useSignal<number | null>(null);
  const loadingMore = useSignal(false);

  // Action state
  const actionMenuOpen = useSignal<string | null>(null); // UID of open menu
  const confirmAction = useSignal<
    {
      actionId: ActionId;
      resource: K8sResource;
      params?: Record<string, unknown>;
    } | null
  >(null);
  const scaleTarget = useSignal<K8sResource | null>(null);
  const scaleValue = useSignal(1);
  const actionLoading = useSignal(false);
  const { rbac } = useAuth();

  const columns = RESOURCE_COLUMNS[kind] ?? [];

  // Namespace for API calls
  const ns = useComputed(() =>
    clusterScoped
      ? ""
      : (selectedNamespace.value === "all" ? "" : selectedNamespace.value)
  );

  // Fetch resources via REST with pagination
  const fetchResources = useCallback(async (append = false) => {
    if (append) {
      loadingMore.value = true;
    } else {
      loading.value = true;
    }
    error.value = null;
    try {
      const basePath = ns.value
        ? `/v1/resources/${kind}/${ns.value}`
        : `/v1/resources/${kind}`;
      const params = new URLSearchParams({ limit: String(PAGE_SIZE) });
      if (append && continueToken.value) {
        params.set("continue", continueToken.value);
      }
      const res = await apiGet<K8sResource[]>(
        `${basePath}?${params.toString()}`,
      );
      const newItems = Array.isArray(res.data) ? res.data : [];
      if (append) {
        // Deduplicate by UID when appending
        const existingUIDs = new Set(items.value.map((r) => r.metadata.uid));
        const unique = newItems.filter((r) =>
          !existingUIDs.has(r.metadata.uid)
        );
        items.value = [...items.value, ...unique];
      } else {
        items.value = newItems;
      }
      continueToken.value = res.metadata?.continue ?? null;
      totalCount.value = res.metadata?.total ?? null;
    } catch (err) {
      error.value = err instanceof Error
        ? err.message
        : "Failed to load resources";
      if (!append) {
        items.value = [];
      }
    } finally {
      loading.value = false;
      loadingMore.value = false;
    }
  }, [kind]);

  // Batched WS event queue — accumulate events and apply once per animation frame (P2-084).
  type WSEvent = { eventType: string; object: unknown };
  const eventQueue = useRef<WSEvent[]>([]);
  const rafId = useRef<number>(0);

  const flushEvents = useCallback(() => {
    rafId.current = 0;
    const batch = eventQueue.current.splice(0);
    if (batch.length === 0) return;

    // Check for resync first — if any event is RESYNC, just re-fetch
    if (batch.some((e) => e.eventType === EVENT_RESYNC)) {
      fetchResources();
      return;
    }

    // Apply all events in a single signal update
    let current = items.value;
    for (const { eventType, object } of batch) {
      if (!object || typeof object !== "object") continue;
      const resource = object as K8sResource;
      const uid = resource.metadata?.uid;
      if (!uid) continue;

      switch (eventType) {
        case EVENT_ADDED:
          if (!current.some((r) => r.metadata.uid === uid)) {
            current = [...current, resource];
          }
          break;
        case EVENT_MODIFIED:
          // Known limitation (todo-095): no resourceVersion comparison — if events
          // arrive out of order during reconnection, an older version could overwrite
          // a newer one. Acceptable for now since the server delivers events in order
          // and reconnection triggers a full REST re-fetch via RESYNC.
          current = current.map((r) => r.metadata.uid === uid ? resource : r);
          break;
        case EVENT_DELETED:
          current = current.filter((r) => r.metadata.uid !== uid);
          break;
      }
    }
    items.value = current;
  }, [kind]);

  // Unified effect: subscribe WS first, then fetch REST to close the event gap.
  useEffect(() => {
    if (!IS_BROWSER) return;

    let unsubscribe: (() => void) | undefined;

    if (enableWS) {
      const subId = `${kind}-${ns.value || "all"}`;
      unsubscribe = subscribe(
        subId,
        kind,
        ns.value,
        (eventType, object) => {
          eventQueue.current.push({ eventType, object });
          if (!rafId.current) {
            rafId.current = requestAnimationFrame(flushEvents);
          }
        },
      );
    }

    // Fetch after subscribing — any events during the fetch are captured above
    fetchResources();

    return () => {
      unsubscribe?.();
      if (rafId.current) {
        cancelAnimationFrame(rafId.current);
        rafId.current = 0;
      }
      eventQueue.current.length = 0;
    };
  }, [kind, ns.value, enableWS]);

  // Compute status string for a resource (used by filter chips)
  const getResourceStatus = useCallback((r: K8sResource): string => {
    if (
      kind === "deployments" || kind === "statefulsets" ||
      kind === "daemonsets"
    ) {
      const dep = r as Deployment;
      const available = dep.status?.availableReplicas ?? 0;
      const replicas = dep.spec?.replicas ?? 0;
      if (available === 0 && replicas > 0) return "failed";
      if (available < replicas) return "progressing";
      return "running";
    }
    if (kind === "pods") {
      const phase = (r as Pod).status?.phase ?? "Unknown";
      if (phase === "Running" || phase === "Succeeded") return "running";
      if (phase === "Pending") return "pending";
      return "failed";
    }
    return "running";
  }, [kind]);

  // Determine which filter chips to show
  const filterChipKinds = new Set([
    "deployments",
    "statefulsets",
    "daemonsets",
  ]);
  const podFilterKind = kind === "pods";
  const showFilterChips = filterChipKinds.has(kind) || podFilterKind;
  const filterChips = podFilterKind
    ? ["All", "Running", "Pending", "Failed"]
    : ["All", "Running", "Progressing", "Failed"];

  // Client-side filter + sort
  const displayed = useComputed(() => {
    let filtered = items.value;

    // Search filter
    const q = search.value.toLowerCase().trim();
    if (q) {
      filtered = filtered.filter((r) => {
        const name = r.metadata.name.toLowerCase();
        const namespace = (r.metadata.namespace ?? "").toLowerCase();
        return name.includes(q) || namespace.includes(q);
      });
    }

    // Status filter
    if (statusFilter.value !== "all" && showFilterChips) {
      filtered = filtered.filter(
        (r) => getResourceStatus(r) === statusFilter.value,
      );
    }

    // Sort
    const key = sortKey.value;
    const dir = sortDir.value === "asc" ? 1 : -1;
    return [...filtered].sort((a, b) => {
      let va: string;
      let vb: string;
      if (key === "name") {
        va = a.metadata.name;
        vb = b.metadata.name;
      } else if (key === "namespace") {
        va = a.metadata.namespace ?? "";
        vb = b.metadata.namespace ?? "";
      } else if (key === "age") {
        va = a.metadata.creationTimestamp;
        vb = b.metadata.creationTimestamp;
      } else {
        va = a.metadata.name;
        vb = b.metadata.name;
      }
      return va < vb ? -dir : va > vb ? dir : 0;
    });
  });

  // Navigate to detail page on row click
  const handleRowClick = useCallback((item: K8sResource) => {
    const basePath = RESOURCE_DETAIL_PATHS[kind];
    if (!basePath) return;
    const isClusterScoped = CLUSTER_SCOPED_KINDS.has(kind);
    const url = isClusterScoped
      ? `${basePath}/${item.metadata.name}`
      : `${basePath}/${item.metadata.namespace}/${item.metadata.name}`;
    globalThis.location.href = url;
  }, [kind]);

  // Close action menu when clicking outside
  useEffect(() => {
    if (!IS_BROWSER || !actionMenuOpen.value) return;
    const handler = () => {
      actionMenuOpen.value = null;
    };
    globalThis.addEventListener("click", handler);
    return () => globalThis.removeEventListener("click", handler);
  }, [actionMenuOpen.value]);

  // Action execution — guarded against concurrent invocation
  const runAction = async (
    actionId: ActionId,
    resource: K8sResource,
    params?: Record<string, unknown>,
  ) => {
    if (actionLoading.value) return;
    actionLoading.value = true;
    try {
      const message = await executeAction(
        actionId,
        kind,
        resource.metadata.namespace ?? "",
        resource.metadata.name,
        params,
      );
      showToast(message, "success");
      confirmAction.value = null;
      scaleTarget.value = null;
    } catch (err) {
      const msg = err instanceof Error ? err.message : "Action failed";
      showToast(msg, "error");
    } finally {
      actionLoading.value = false;
    }
  };

  const handleActionClick = (actionId: ActionId, resource: K8sResource) => {
    if (actionLoading.value) return;
    actionMenuOpen.value = null;
    const meta = getActionMeta(actionId, resource);

    if (actionId === "scale") {
      const spec = resource.spec as { replicas?: number } | undefined;
      scaleValue.value = spec?.replicas ?? 1;
      scaleTarget.value = resource;
      return;
    }

    if (meta.confirm) {
      // Pre-compute action params so the confirm dialog onClick is simple
      let params: Record<string, unknown> | undefined;
      if (actionId === "suspend") {
        const spec = resource.spec as { suspend?: boolean } | undefined;
        params = { suspend: !spec?.suspend };
      }
      confirmAction.value = { actionId, resource, params };
      return;
    }

    runAction(actionId, resource);
  };

  // Item count display — show "X of Y" when total is known and more exist
  const itemCountText = useComputed(() => {
    if (loading.value) return "Loading...";
    const shown = displayed.value.length;
    const total = totalCount.value;
    if (total !== null && total > items.value.length) {
      return `${shown} shown (${items.value.length} of ${total} loaded)`;
    }
    return `${shown} items`;
  });

  // Filter actions by k8s permissions for the current namespace
  const actions = useComputed(() =>
    getVisibleActions(kind, ns.value, rbac.value)
  );

  // Kebab menu renderer for each row — reads actions.value reactively inside the callback
  const renderKebab = (resource: K8sResource) => {
    const currentActions = actions.value;
    if (currentActions.length === 0) return null;
    const isOpen = actionMenuOpen.value === resource.metadata.uid;
    return (
      <div class="relative">
        <button
          type="button"
          onClick={(e) => {
            e.stopPropagation();
            actionMenuOpen.value = isOpen ? null : resource.metadata.uid;
          }}
          class="rounded p-1 text-text-muted hover:bg-hover hover:text-text-primary"
          title="Actions"
        >
          <svg class="h-4 w-4" viewBox="0 0 16 16" fill="currentColor">
            <circle cx="8" cy="3" r="1.5" />
            <circle cx="8" cy="8" r="1.5" />
            <circle cx="8" cy="13" r="1.5" />
          </svg>
        </button>
        {isOpen && (
          <div
            class="absolute right-0 z-20 mt-1 w-40 rounded-md border border-border-primary bg-surface py-1 shadow-lg"
            onClick={(e) => e.stopPropagation()}
          >
            {currentActions.map((actionId: ActionId) => {
              const meta = getActionMeta(actionId, resource);
              return (
                <button
                  key={actionId}
                  type="button"
                  onClick={() => handleActionClick(actionId, resource)}
                  class={`w-full px-3 py-1.5 text-left text-sm ${
                    meta.danger
                      ? "text-danger hover:bg-danger-dim"
                      : "text-text-secondary hover:bg-hover"
                  }`}
                >
                  {meta.label}
                </button>
              );
            })}
          </div>
        )}
      </div>
    );
  };

  // Map RESOURCE_COLUMNS (DataTable format) → UIColumn[] for ResourceTable
  const uiColumns: UIColumn[] = columns.map((col) => ({
    key: col.key,
    label: col.label,
    // Right-align numeric/age columns
    align: (col.key === "age" || col.key === "restarts" ||
        col.key === "replicas" || col.key === "completions" ||
        col.key === "active" || col.key === "upToDate")
      ? "right"
      : "left",
  }));

  // Build UIRow[] from K8sResource[] using the existing render functions
  const uiRows = useComputed((): UIRow[] => {
    const currentActions = actions.value;
    return displayed.value.map((resource): UIRow => {
      const cells: Record<string, import("preact").ComponentChildren> = {};
      for (const col of columns) {
        cells[col.key] = col.render ? col.render(resource) : String(
          (resource as Record<string, unknown>)[col.key] ?? "",
        );
      }
      // Inject kebab menu as last "actions" cell when there are actions
      if (currentActions.length > 0) {
        cells["_actions"] = renderKebab(resource);
      }
      return {
        id: resource.metadata.uid,
        cells,
        onClick: () => handleRowClick(resource),
      };
    });
  });

  // Columns including optional actions column
  const uiColumnsWithActions = useComputed((): UIColumn[] => {
    if (actions.value.length === 0) return uiColumns;
    return [...uiColumns, { key: "_actions", label: "", width: "40px" }];
  });

  // Confirmation dialog
  const confirmMeta = confirmAction.value
    ? getActionMeta(
      confirmAction.value.actionId,
      confirmAction.value.resource,
    )
    : null;
  const isDestructive = confirmMeta?.confirm === "destructive";
  const confirmName = confirmAction.value?.resource.metadata.name ?? "";

  // Empty / loading state rows
  const emptyRows: UIRow[] = loading.value
    ? [
      {
        id: "__loading__",
        cells: Object.fromEntries(
          uiColumns.map((c) => [
            c.key,
            <span
              key={c.key}
              style={{
                display: "block",
                height: "12px",
                width: "80px",
                background: "var(--bg-elevated)",
                borderRadius: "4px",
                opacity: 0.6,
              }}
            />,
          ]),
        ),
      },
    ]
    : [];

  const finalRows = loading.value ? emptyRows : uiRows.value;

  return (
    <div class="space-y-4">
      {/* Page header — shown when not nested in a parent dashboard */}
      {!hideHeader && (
        <div
          style={{
            display: "flex",
            alignItems: "flex-start",
            justifyContent: "space-between",
            gap: "16px",
            marginBottom: "4px",
          }}
        >
          <div>
            <h1
              style={{
                margin: 0,
                fontSize: "24px",
                fontWeight: 700,
                color: "var(--text-primary)",
                lineHeight: 1.2,
              }}
            >
              {title}
            </h1>
            <p
              style={{
                margin: "4px 0 0",
                fontSize: "13px",
                color: "var(--text-muted)",
              }}
            >
              {itemCountText.value}
            </p>
          </div>
          <div style={{ display: "flex", alignItems: "center", gap: "8px" }}>
            <button
              type="button"
              onClick={() => fetchResources()}
              style={{
                display: "flex",
                alignItems: "center",
                justifyContent: "center",
                width: "32px",
                height: "32px",
                border: "1px solid var(--border-primary)",
                borderRadius: "8px",
                background: "transparent",
                color: "var(--text-muted)",
                cursor: "pointer",
              }}
              title="Refresh"
            >
              <svg
                style={{
                  width: "15px",
                  height: "15px",
                  animation: loading.value ? "spin 1s linear infinite" : "none",
                }}
                viewBox="0 0 16 16"
                fill="none"
                stroke="currentColor"
                stroke-width="1.5"
              >
                <path d="M14 8A6 6 0 1 1 8 2" />
                <path d="M14 2v4h-4" />
              </svg>
            </button>
            {createHref &&
              canPerformCheck(rbac.value, kind, "create", ns.value) && (
              <a
                href={createHref}
                style={{
                  display: "inline-flex",
                  alignItems: "center",
                  gap: "6px",
                  padding: "8px 16px",
                  fontSize: "13px",
                  fontWeight: 600,
                  color: "var(--bg-base)",
                  background: "var(--accent)",
                  borderRadius: "9px",
                  textDecoration: "none",
                  border: "none",
                  cursor: "pointer",
                  whiteSpace: "nowrap",
                }}
              >
                <svg
                  width="14"
                  height="14"
                  viewBox="0 0 16 16"
                  fill="none"
                  stroke="currentColor"
                  stroke-width="2.5"
                >
                  <path d="M4 8h8M8 4v8" />
                </svg>
                New {title.replace(/s$/, "")}
              </a>
            )}
          </div>
        </div>
      )}

      {/* Error state */}
      {error.value && <ErrorBanner message={error.value} />}

      {/* Toolbar */}
      <div
        style={{
          display: "flex",
          alignItems: "center",
          gap: "10px",
          marginBottom: "8px",
        }}
      >
        <div style={{ maxWidth: "280px", flex: "1 1 auto" }}>
          <SearchBar
            value={search.value}
            onInput={(v) => {
              search.value = v;
            }}
            placeholder={`Search ${title.toLowerCase()}...`}
          />
        </div>
        {showFilterChips && (
          <div
            style={{ display: "flex", gap: "6px", alignItems: "center" }}
          >
            {filterChips.map((chip) => {
              const val = chip.toLowerCase();
              const isActive = statusFilter.value === val;
              return (
                <button
                  key={chip}
                  type="button"
                  onClick={() => {
                    statusFilter.value = val;
                  }}
                  style={{
                    padding: "4px 10px",
                    borderRadius: "9px",
                    fontSize: "11px",
                    fontWeight: 600,
                    background: isActive
                      ? "color-mix(in srgb, var(--accent) 14%, transparent)"
                      : "var(--bg-elevated)",
                    border: `1px solid ${
                      isActive ? "var(--accent)" : "var(--border-primary)"
                    }`,
                    color: isActive ? "var(--accent)" : "var(--text-muted)",
                    cursor: "pointer",
                    transition: "all 120ms ease",
                  }}
                >
                  {chip}
                </button>
              );
            })}
          </div>
        )}
        <span
          style={{
            marginLeft: "auto",
            fontSize: "12px",
            color: "var(--text-muted)",
            fontFamily: "var(--font-mono, monospace)",
          }}
        >
          {displayed.value.length} {kind}
        </span>
      </div>

      {/* ResourceTable — solid surface (bg-surface), glass rule: data = solid */}
      {finalRows.length === 0 && !loading.value
        ? (
          <div
            style={{
              background: "var(--bg-surface)",
              border: "1px solid var(--border-primary)",
              borderRadius: "16px",
              padding: "48px",
              textAlign: "center",
              color: "var(--text-muted)",
              fontSize: "13px",
            }}
          >
            No {title.toLowerCase()} found
          </div>
        )
        : (
          <ResourceTable
            columns={uiColumnsWithActions.value}
            rows={finalRows}
            chevron
          />
        )}

      {/* Load More */}
      {continueToken.value && (
        <div class="flex justify-center">
          <button
            type="button"
            onClick={() => fetchResources(true)}
            disabled={loadingMore.value}
            style={{
              padding: "8px 20px",
              fontSize: "13px",
              fontWeight: 500,
              color: "var(--text-muted)",
              background: "var(--bg-surface)",
              border: "1px solid var(--border-primary)",
              borderRadius: "9px",
              cursor: loadingMore.value ? "not-allowed" : "pointer",
              opacity: loadingMore.value ? 0.5 : 1,
            }}
          >
            {loadingMore.value ? "Loading..." : "Load more"}
          </button>
        </div>
      )}

      {/* Confirm Dialog */}
      {confirmAction.value && confirmMeta && (
        <ConfirmDialog
          title={`${confirmMeta.label} ${confirmAction.value.resource.metadata.name}`}
          message={confirmMeta.confirmMessage}
          confirmLabel={confirmMeta.label}
          danger={confirmMeta.danger}
          typeToConfirm={isDestructive ? confirmName : undefined}
          loading={actionLoading.value}
          onConfirm={() =>
            runAction(
              confirmAction.value!.actionId,
              confirmAction.value!.resource,
              confirmAction.value!.params,
            )}
          onCancel={() => {
            confirmAction.value = null;
          }}
        />
      )}

      {/* Scale Dialog */}
      {scaleTarget.value && (
        <ScaleDialog
          resourceName={scaleTarget.value.metadata.name}
          currentReplicas={(scaleTarget.value.spec as
            | { replicas?: number }
            | undefined)?.replicas}
          value={scaleValue.value}
          onValueChange={(v) => {
            scaleValue.value = v;
          }}
          loading={actionLoading.value}
          onConfirm={() =>
            runAction("scale", scaleTarget.value!, {
              replicas: scaleValue.value,
            })}
          onCancel={() => {
            scaleTarget.value = null;
          }}
        />
      )}
    </div>
  );
}
