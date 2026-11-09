/** @jsxImportSource react */
import { batch } from "@preact/signals-core";
import {
  type ReactNode,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog.tsx";
import { ErrorBanner } from "@/components/ui/ErrorBanner.tsx";
import ResourceTable, {
  type Column as UIColumn,
  type Row as UIRow,
} from "@/components/ui/ResourceTable.tsx";
import { ScaleDialog } from "@/components/ui/ScaleDialog.tsx";
import { SearchBar } from "@/components/ui/SearchBar.tsx";
import type { ActionId } from "@/lib/action-handlers.ts";
import {
  executeAction,
  getActionMeta,
  getVisibleActions,
} from "@/lib/action-handlers.ts";
import { apiGet } from "@/lib/api.ts";
import { useAuth } from "@/lib/auth.ts";
import {
  CLUSTER_SCOPED_KINDS,
  RESOURCE_DETAIL_PATHS,
} from "@/lib/constants.ts";
import type { Deployment, K8sResource, Pod } from "@/lib/k8s-types.ts";
import { canPerform as canPerformCheck } from "@/lib/permissions.ts";
import {
  SAVED_VIEW_SORT_KEYS,
  type TableViewState,
} from "@/lib/preference-types.ts";
import { RESOURCE_COLUMNS } from "@/lib/resource-columns.ts";
import SavedViews from "@/src/islands/SavedViews.tsx";
import { LOCAL_CLUSTER_ID, selectedCluster } from "@/src/lib/cluster.ts";
import { IS_BROWSER } from "@/src/lib/is-browser.ts";
import { selectedNamespace } from "@/src/lib/namespace.ts";
import { getCount, resourceCounts } from "@/src/lib/resource-counts.ts";
import { showToast } from "@/src/lib/toast-store.ts";
import { useLatest } from "@/src/lib/use-latest.ts";
import { useSignalValue } from "@/src/lib/use-signal-value.ts";
import {
  EVENT_ADDED,
  EVENT_DELETED,
  EVENT_MODIFIED,
  EVENT_RESYNC,
  subscribe,
} from "@/src/lib/ws.ts";

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

/**
 * The status chip a link asked for with `?status=`, or null. The dashboard's
 * "Failed pods" row opens `/workloads/pods?status=failed`. Only a chip this
 * table offers is honoured, so an unknown value leaves the filter on "all".
 */
export function statusFilterFromQuery(
  search: string,
  chips: readonly string[],
): string | null {
  const wanted = new URLSearchParams(search).get("status")?.toLowerCase();
  if (!wanted) return null;
  return chips.some((c) => c.toLowerCase() === wanted) ? wanted : null;
}

/** Kubernetes namespace names are DNS labels (RFC 1123). */
const NAMESPACE_NAME = /^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$/;

/**
 * The namespace a link asked for with `?namespace=`: "all" or a namespace
 * name, or null when the parameter is absent or not a valid name. The
 * dashboard's "Failed pods" row sends `namespace=all`, because its count
 * covers every namespace.
 */
export function namespaceFromQuery(search: string): string | null {
  const ns = new URLSearchParams(search).get("namespace");
  if (!ns) return null;
  return ns === "all" || NAMESPACE_NAME.test(ns) ? ns : null;
}

/** The notice shown when this table changes the app-wide namespace. */
export function namespaceSwitchNotice(ns: string): string {
  return `Namespace switched to ${
    ns === "all" ? "all namespaces" : ns
  } for every page, not just this table.`;
}

/** Removes one query parameter from the address bar, keeping the rest. */
function dropQueryParam(name: string): void {
  const url = new URL(globalThis.location.href);
  if (!url.searchParams.has(name)) return;
  url.searchParams.delete(name);
  globalThis.history.replaceState(globalThis.history.state, "", url);
}

/**
 * The pod phases each pod status chip holds. Failed means phase Failed only,
 * the same set the dashboard's "Failed pods" count reads (`dashboard.go`).
 * Unknown (the node lost contact) is not a failure: it is in no chip and
 * shows under All.
 */
const POD_CHIP_PHASES: Record<string, readonly string[]> = {
  running: ["Running", "Succeeded"],
  pending: ["Pending"],
  failed: ["Failed"],
};

/**
 * The phases to ask the server for when a pod chip is active, sent as
 * repeated `?phase=`. The server filters before it paginates, so a chip
 * covers the whole list, not just the pages already loaded. Empty for All.
 */
export function podPhasesForChip(chip: string): readonly string[] {
  return POD_CHIP_PHASES[chip] ?? [];
}

/** The status chip a pod falls under, from its phase, or "unknown". */
export function podChipStatus(phase: string | undefined): string {
  for (const [chip, phases] of Object.entries(POD_CHIP_PHASES)) {
    if (phase !== undefined && phases.includes(phase)) return chip;
  }
  return "unknown";
}

/**
 * Writes the status chip into the address bar: `?status=<chip>`, or no
 * parameter for "all". Other parameters and the history entry's state are
 * kept, and nothing is written when the URL would not change.
 */
export function syncStatusParam(status: string): void {
  const url = new URL(globalThis.location.href);
  if (status === "all") url.searchParams.delete("status");
  else url.searchParams.set("status", status);
  if (url.href === globalThis.location.href) return;
  globalThis.history.replaceState(globalThis.history.state, "", url);
}

/**
 * The toolbar's row count, or null when it would only repeat the header.
 *
 * A filter that narrows the list always gets "N of M match". Otherwise the
 * count is shown only when nothing above the table gives one: the page hides
 * this table's header and its own header has no count, because the counts
 * API leaves the kind out (Secrets are never counted, and a kind the user may
 * not list is omitted).
 */
export function toolbarCountText(opts: {
  shown: number;
  loaded: number;
  hideHeader: boolean;
  headerHasCount: boolean;
  title: string;
}): string | null {
  if (opts.shown !== opts.loaded) {
    return `${opts.shown} of ${opts.loaded} match`;
  }
  if (opts.hideHeader && !opts.headerHasCount) {
    return `${opts.loaded} ${opts.title.toLowerCase()}`;
  }
  return null;
}

/**
 * Per-row kebab action menu. Defined at module scope so its markup stays out
 * of the island's own render.
 */
function KebabCell({
  resource,
  actions,
  openUid,
  setOpenUid,
  onAction,
}: {
  resource: K8sResource;
  actions: ActionId[];
  openUid: string | null;
  setOpenUid: (uid: string | null) => void;
  onAction: (actionId: ActionId, resource: K8sResource) => void;
}) {
  if (actions.length === 0) return null;
  const isOpen = openUid === resource.metadata.uid;
  return (
    <div className="relative">
      <button
        type="button"
        onClick={(e) => {
          e.stopPropagation();
          setOpenUid(isOpen ? null : resource.metadata.uid);
        }}
        className="rounded p-1 text-text-muted hover:bg-hover hover:text-text-primary"
        title="Actions"
      >
        <svg className="h-4 w-4" viewBox="0 0 16 16" fill="currentColor">
          <circle cx="8" cy="3" r="1.5" />
          <circle cx="8" cy="8" r="1.5" />
          <circle cx="8" cy="13" r="1.5" />
        </svg>
      </button>
      {isOpen && (
        <div
          className="absolute right-0 z-20 mt-1 w-40 rounded-md border border-border-primary bg-surface py-1 shadow-lg"
          onClick={(e) => e.stopPropagation()}
        >
          {actions.map((actionId: ActionId) => {
            const meta = getActionMeta(actionId, resource);
            return (
              <button
                key={actionId}
                type="button"
                onClick={() => onAction(actionId, resource)}
                className={`w-full px-3 py-1.5 text-left text-sm ${
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
}

/** The filters a saved view captures, always changed together. */
interface TableFilters {
  search: string;
  statusFilter: string;
  sortKey: string;
  sortDir: "asc" | "desc";
}

interface ConfirmState {
  actionId: ActionId;
  resource: K8sResource;
  params?: Record<string, unknown>;
}

type WSEvent = { eventType: string; object: unknown };

const FILTER_CHIP_KINDS = new Set([
  "deployments",
  "statefulsets",
  "daemonsets",
]);
const POD_FILTER_CHIPS = ["All", "Running", "Pending", "Failed"];
const WORKLOAD_FILTER_CHIPS = ["All", "Running", "Progressing", "Failed"];

export default function ResourceTableIsland({
  kind,
  title,
  clusterScoped = false,
  enableWS = true,
  hideHeader = false,
  createHref,
}: ResourceTableIslandProps) {
  const [items, setItems] = useState<K8sResource[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [filters, setFilters] = useState<TableFilters>({
    search: "",
    statusFilter: "all",
    sortKey: "name",
    sortDir: "asc",
  });
  // Written with every filter change, ahead of the next render, so the fetch
  // that a mount-time link filter triggers reads the chip just chosen rather
  // than the one last rendered.
  const filtersRef = useRef(filters);
  const { search, statusFilter, sortKey, sortDir } = filters;
  // Degradations reported by applyViewState when a saved view is restored.
  // Surfaced above the table and dismissible; never swallowed, because a view
  // that quietly restored something other than what was saved is worse than
  // one that says what it could not honour.
  const [restoreWarnings, setRestoreWarnings] = useState<string[]>([]);
  const [continueToken, setContinueToken] = useState<string | null>(null);
  const continueTokenRef = useLatest(continueToken);
  const [totalCount, setTotalCount] = useState<number | null>(null);
  const [loadingMore, setLoadingMore] = useState(false);

  // Action state
  const [actionMenuUid, setActionMenuUid] = useState<string | null>(null);
  const [confirmAction, setConfirmAction] = useState<ConfirmState | null>(null);
  const [scaleTarget, setScaleTarget] = useState<K8sResource | null>(null);
  const [scaleValue, setScaleValue] = useState(1);
  const [actionLoading, setActionLoading] = useState(false);
  // Stops a second click before the next render shows actionLoading.
  const actionBusy = useRef(false);
  const { rbac } = useAuth();
  const rbacValue = useSignalValue(rbac);
  const namespace = useSignalValue(selectedNamespace);
  const cluster = useSignalValue(selectedCluster);
  const counts = useSignalValue(resourceCounts);

  const columns = useMemo(() => RESOURCE_COLUMNS[kind] ?? [], [kind]);

  // Determine which filter chips to show
  const podFilterKind = kind === "pods";
  const showFilterChips = FILTER_CHIP_KINDS.has(kind) || podFilterKind;
  const filterChips = podFilterKind ? POD_FILTER_CHIPS : WORKLOAD_FILTER_CHIPS;

  const updateFilters = useCallback(
    (change: (prev: TableFilters) => TableFilters) => {
      const next = change(filtersRef.current);
      filtersRef.current = next;
      setFilters(next);
    },
    [],
  );

  /**
   * The one write path for the status filter. A chip click and a saved-view
   * restore both go through here, so the URL always names the chip on screen
   * and a reload or a shared link reopens the same list.
   */
  const setStatusFilter = (val: string) => {
    updateFilters((prev) => ({ ...prev, statusFilter: val }));
    if (IS_BROWSER && showFilterChips) syncStatusParam(val);
  };
  const setStatusFilterRef = useLatest(setStatusFilter);

  // Namespace for API calls
  const ns = clusterScoped ? "" : namespace === "all" ? "" : namespace;

  // What a saved view would capture right now. Read live, so "Save" stores
  // what is on screen rather than what was on screen when the menu opened.
  const viewState = useMemo<TableViewState>(
    () => ({
      resourceKind: kind,
      namespace: ns,
      search,
      statusFilter,
      sortKey,
      sortDir,
    }),
    [kind, ns, search, statusFilter, sortKey, sortDir],
  );

  /**
   * Applies a restored view to this table.
   *
   * Restoring a namespace writes selectedNamespace, which is app-global: every
   * other page follows it. That is a bigger effect than "this table changed",
   * so the notice says so, and it is only written when the stored namespace
   * actually differs from the current one.
   *
   * The table's filters change in one write and the namespace write is batched
   * with it, so the effect keyed on [kind, ns, enableWS] runs once rather than
   * once per field. The existing fetchAbort controller cancels the superseded
   * load; no second cancellation path.
   */
  const applyView = useCallback(
    (state: TableViewState, warnings: string[]) => {
      const restoredNs = state.namespace === "" ? "all" : state.namespace;
      const namespaceChanges =
        !clusterScoped && restoredNs !== selectedNamespace.value;

      const notices = [...warnings];
      if (namespaceChanges) {
        notices.push(namespaceSwitchNotice(restoredNs));
      }

      batch(() => {
        updateFilters((prev) => ({
          ...prev,
          search: state.search,
          sortKey: state.sortKey,
          sortDir: state.sortDir === "desc" ? "desc" : "asc",
        }));
        setStatusFilterRef.current(state.statusFilter);
        setRestoreWarnings(notices);
        if (namespaceChanges) {
          selectedNamespace.value = restoredNs;
        }
      });
    },
    [clusterScoped, updateFilters],
  );

  /**
   * Opens the table on the filters a link names.
   *
   * `?status=` picks the chip; setStatusFilter keeps it in the URL from then
   * on. `?namespace=` switches the app-wide namespace, as restoring a saved
   * view does, so it shows the same notice. It is then dropped from the URL:
   * the selection persists on its own, and a parameter left behind would undo
   * the user's next namespace pick on reload. Declared before the fetch
   * effect so the first load already uses both.
   */
  useEffect(() => {
    if (!IS_BROWSER) return;
    const query = globalThis.location.search;
    const wantedStatus = showFilterChips
      ? statusFilterFromQuery(query, filterChips)
      : null;
    const wantedNs = clusterScoped ? null : namespaceFromQuery(query);
    batch(() => {
      if (wantedStatus !== null) {
        updateFilters((prev) => ({ ...prev, statusFilter: wantedStatus }));
      }
      if (wantedNs !== null && wantedNs !== selectedNamespace.value) {
        selectedNamespace.value = wantedNs;
        setRestoreWarnings([namespaceSwitchNotice(wantedNs)]);
      }
    });
    if (!clusterScoped) dropQueryParam("namespace");
  }, [clusterScoped, showFilterChips, filterChips, updateFilters]);

  // The namespace requests address, read from the store itself: a link that
  // switches the namespace on mount writes it before the render that follows.
  const liveNamespace = useCallback(() => {
    if (clusterScoped) return "";
    const selected = selectedNamespace.value;
    return selected === "all" ? "" : selected;
  }, [clusterScoped]);

  // Fetch resources via REST with pagination
  const fetchAbort = useRef<AbortController | null>(null);
  const fetchResources = useCallback(
    async (append = false) => {
      // Cancel any in-flight fetch so a slow response for a previous
      // namespace/cluster can't land late and overwrite current data.
      fetchAbort.current?.abort();
      const ac = new AbortController();
      fetchAbort.current = ac;

      if (append) {
        setLoadingMore(true);
      } else {
        setLoading(true);
      }
      setError(null);
      try {
        const liveNs = liveNamespace();
        const basePath = liveNs
          ? `/v1/resources/${kind}/${liveNs}`
          : `/v1/resources/${kind}`;
        const params = new URLSearchParams({ limit: String(PAGE_SIZE) });
        if (kind === "pods") {
          for (const phase of podPhasesForChip(
            filtersRef.current.statusFilter,
          )) {
            params.append("phase", phase);
          }
        }
        if (append && continueTokenRef.current) {
          params.set("continue", continueTokenRef.current);
        }
        const res = await apiGet<K8sResource[]>(
          `${basePath}?${params.toString()}`,
          ac.signal,
        );
        if (ac.signal.aborted) return;
        const newItems = Array.isArray(res.data) ? res.data : [];
        if (append) {
          // Deduplicate by UID when appending
          setItems((prev) => {
            const existingUIDs = new Set(prev.map((r) => r.metadata.uid));
            const unique = newItems.filter(
              (r) => !existingUIDs.has(r.metadata.uid),
            );
            return [...prev, ...unique];
          });
        } else {
          setItems(newItems);
        }
        setContinueToken(res.metadata?.continue ?? null);
        setTotalCount(res.metadata?.total ?? null);
      } catch (err) {
        // Ignore aborts — a newer fetch (or unmount) superseded this one.
        if (
          ac.signal.aborted ||
          (err instanceof DOMException && err.name === "AbortError")
        ) {
          return;
        }
        setError(
          err instanceof Error ? err.message : "Failed to load resources",
        );
        if (!append) {
          setItems([]);
        }
      } finally {
        // Only the latest fetch owns the loading flags.
        if (fetchAbort.current === ac) {
          setLoading(false);
          setLoadingMore(false);
        }
      }
    },
    [kind, liveNamespace],
  );

  // Batched WS event queue — accumulate events and apply once per animation frame (P2-084).
  const eventQueue = useRef<WSEvent[]>([]);
  const rafId = useRef<number>(0);

  const flushEvents = useCallback(() => {
    rafId.current = 0;
    const pending = eventQueue.current.splice(0);
    if (pending.length === 0) return;

    // Check for resync first — if any event is RESYNC, just re-fetch
    if (pending.some((e) => e.eventType === EVENT_RESYNC)) {
      fetchResources();
      return;
    }

    // Apply all events in a single update, on the rows as they are now
    setItems((prev) => {
      let current = prev;
      for (const { eventType, object } of pending) {
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
            current = current.map((r) =>
              r.metadata.uid === uid ? resource : r,
            );
            break;
          case EVENT_DELETED:
            current = current.filter((r) => r.metadata.uid !== uid);
            break;
        }
      }
      return current;
    });
  }, [fetchResources]);

  // A pod chip is a server-side filter (?phase=), so changing it reloads the
  // list from the first page. Other kinds' chips filter loaded rows only. The
  // client-side check in `displayed` stays for every kind: live WS events add
  // and change pods whatever their phase.
  const podChip = kind === "pods" ? statusFilter : "";

  // Unified effect: subscribe WS first, then fetch REST to close the event gap.
  useEffect(() => {
    if (!IS_BROWSER) return;
    // This render predates a namespace or pod chip a link just set on mount.
    // The render that follows carries them, so wait for it rather than load
    // and subscribe for the old scope first.
    if (
      ns !== liveNamespace() ||
      podChip !== (kind === "pods" ? filtersRef.current.statusFilter : "")
    ) {
      return;
    }

    let unsubscribe: (() => void) | undefined;

    // The resource WebSocket carries no cluster dimension: lib/ws.ts opens one
    // fixed /ws/v1/ws/resources URL and the backend feeds it from the LOCAL
    // cluster's informers (remote clusters have no informers and emit no
    // events). Subscribing while a remote cluster is selected would splice
    // local-cluster events into a remote-cluster table. Live updates are
    // therefore local-only; a remote cluster shows fetched data without them.
    if (enableWS && cluster === LOCAL_CLUSTER_ID) {
      const subId = `${kind}-${ns || "all"}`;
      unsubscribe = subscribe(subId, kind, ns, (eventType, object) => {
        eventQueue.current.push({ eventType, object });
        if (!rafId.current) {
          rafId.current = requestAnimationFrame(flushEvents);
        }
      });
    }

    // Fetch after subscribing — any events during the fetch are captured above
    fetchResources();

    return () => {
      unsubscribe?.();
      fetchAbort.current?.abort();
      if (rafId.current) {
        cancelAnimationFrame(rafId.current);
        rafId.current = 0;
      }
      eventQueue.current.length = 0;
    };
  }, [
    kind,
    ns,
    enableWS,
    cluster,
    podChip,
    flushEvents,
    fetchResources,
    liveNamespace,
  ]);

  // Compute status string for a resource (used by filter chips)
  const getResourceStatus = useCallback(
    (r: K8sResource): string => {
      if (
        kind === "deployments" ||
        kind === "statefulsets" ||
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
        return podChipStatus((r as Pod).status?.phase);
      }
      return "running";
    },
    [kind],
  );

  // Client-side filter + sort
  const displayed = useMemo(() => {
    let filtered = items;

    // Search filter
    const q = search.toLowerCase().trim();
    if (q) {
      filtered = filtered.filter((r) => {
        const name = r.metadata.name.toLowerCase();
        const namespace = (r.metadata.namespace ?? "").toLowerCase();
        return name.includes(q) || namespace.includes(q);
      });
    }

    // Status filter
    if (statusFilter !== "all" && showFilterChips) {
      filtered = filtered.filter((r) => getResourceStatus(r) === statusFilter);
    }

    // Sort
    const dir = sortDir === "asc" ? 1 : -1;
    return [...filtered].sort((a, b) => {
      let va: string;
      let vb: string;
      if (sortKey === "name") {
        va = a.metadata.name;
        vb = b.metadata.name;
      } else if (sortKey === "namespace") {
        va = a.metadata.namespace ?? "";
        vb = b.metadata.namespace ?? "";
      } else if (sortKey === "age") {
        va = a.metadata.creationTimestamp;
        vb = b.metadata.creationTimestamp;
      } else {
        va = a.metadata.name;
        vb = b.metadata.name;
      }
      return va < vb ? -dir : va > vb ? dir : 0;
    });
  }, [
    items,
    search,
    statusFilter,
    showFilterChips,
    getResourceStatus,
    sortKey,
    sortDir,
  ]);

  // Navigate to detail page on row click
  const handleRowClick = useCallback(
    (item: K8sResource) => {
      const basePath = RESOURCE_DETAIL_PATHS[kind];
      if (!basePath) return;
      const isClusterScoped = CLUSTER_SCOPED_KINDS.has(kind);
      const url = isClusterScoped
        ? `${basePath}/${item.metadata.name}`
        : `${basePath}/${item.metadata.namespace}/${item.metadata.name}`;
      globalThis.location.href = url;
    },
    [kind],
  );

  // Close action menu when clicking outside
  const menuIsOpen = actionMenuUid !== null;
  useEffect(() => {
    if (!IS_BROWSER || !menuIsOpen) return;
    const handler = () => {
      setActionMenuUid(null);
    };
    globalThis.addEventListener("click", handler);
    return () => globalThis.removeEventListener("click", handler);
  }, [menuIsOpen]);

  // Action execution — guarded against concurrent invocation
  const runAction = async (
    actionId: ActionId,
    resource: K8sResource,
    params?: Record<string, unknown>,
  ) => {
    if (actionBusy.current) return;
    actionBusy.current = true;
    setActionLoading(true);
    try {
      const message = await executeAction(
        actionId,
        kind,
        resource.metadata.namespace ?? "",
        resource.metadata.name,
        params,
      );
      showToast(message, "success");
      setConfirmAction(null);
      setScaleTarget(null);
    } catch (err) {
      const msg = err instanceof Error ? err.message : "Action failed";
      showToast(msg, "error");
    } finally {
      actionBusy.current = false;
      setActionLoading(false);
    }
  };

  const handleActionClick = (actionId: ActionId, resource: K8sResource) => {
    if (actionBusy.current) return;
    setActionMenuUid(null);
    const meta = getActionMeta(actionId, resource, kind);

    if (actionId === "scale") {
      const spec = resource.spec as { replicas?: number } | undefined;
      setScaleValue(spec?.replicas ?? 1);
      setScaleTarget(resource);
      return;
    }

    if (meta.confirm) {
      // Pre-compute action params so the confirm dialog onClick is simple
      let params: Record<string, unknown> | undefined;
      if (actionId === "suspend") {
        const spec = resource.spec as { suspend?: boolean } | undefined;
        params = { suspend: !spec?.suspend };
      }
      setConfirmAction({ actionId, resource, params });
      return;
    }

    runAction(actionId, resource);
  };

  // Item count display — show "X of Y" when total is known and more exist
  let itemCountText: string;
  if (loading) {
    itemCountText = "Loading...";
  } else if (totalCount !== null && totalCount > items.length) {
    itemCountText =
      displayed.length === items.length
        ? `${displayed.length} of ${totalCount} shown`
        : `${displayed.length} shown (${items.length} of ${totalCount} loaded)`;
  } else {
    itemCountText = `${displayed.length} items`;
  }

  // While counts are loading the page header reads "Loading…"; after that it
  // has a count only when the counts API returned one for this kind.
  const toolbarCount = loading
    ? null
    : toolbarCountText({
        shown: displayed.length,
        loaded: items.length,
        hideHeader,
        headerHasCount: counts === null || getCount(kind) !== null,
        title,
      });

  // Filter actions by k8s permissions for the current namespace
  const actions = useMemo(
    () => getVisibleActions(kind, ns, rbacValue),
    [kind, ns, rbacValue],
  );

  // Map RESOURCE_COLUMNS (the Column shape in lib/resource-columns.ts) →
  // UIColumn[] for ResourceTable
  const uiColumns = useMemo<UIColumn[]>(
    () =>
      columns.map((col) => ({
        key: col.key,
        label: col.label,
        // Right-align numeric/age columns
        align:
          col.key === "age" ||
          col.key === "restarts" ||
          col.key === "replicas" ||
          col.key === "completions" ||
          col.key === "active" ||
          col.key === "upToDate"
            ? "right"
            : "left",
        // The displayed-sort comparator only handles these keys, and a saved view
        // may only store one of them. Deriving the gate from that same constant
        // keeps the two from drifting apart: a column made sortable here without
        // widening SAVED_VIEW_SORT_KEYS (and the Go allowlist behind it) would
        // produce a sort the user could not save.
        sortable: (SAVED_VIEW_SORT_KEYS as readonly string[]).includes(col.key),
      })),
    [columns],
  );

  // Toggle/flip sort when a sortable header is clicked.
  const handleSort = (key: string) => {
    updateFilters((prev) =>
      prev.sortKey === key
        ? { ...prev, sortDir: prev.sortDir === "asc" ? "desc" : "asc" }
        : { ...prev, sortKey: key, sortDir: "asc" },
    );
  };

  // The per-row cells that do not depend on which menu is open, so opening a
  // menu or typing in a dialog does not re-render every cell of every row.
  const baseRows = useMemo(
    () =>
      displayed.map((resource) => {
        const cells: Record<string, ReactNode> = {};
        for (const col of columns) {
          cells[col.key] = col.render
            ? col.render(resource)
            : String((resource as Record<string, unknown>)[col.key] ?? "");
        }
        return {
          id: resource.metadata.uid,
          cells,
          onClick: () => handleRowClick(resource),
        };
      }),
    [displayed, columns, handleRowClick],
  );

  const handleActionClickRef = useLatest(handleActionClick);
  const onRowAction = useCallback(
    (actionId: ActionId, resource: K8sResource) =>
      handleActionClickRef.current(actionId, resource),
    [],
  );

  // Inject kebab menu as last "actions" cell when there are actions.
  const uiRows = useMemo<UIRow[]>(() => {
    if (actions.length === 0) return baseRows;
    return baseRows.map((row, i) => {
      const resource = displayed[i] as K8sResource;
      return {
        ...row,
        cells: {
          ...row.cells,
          _actions: (
            <KebabCell
              resource={resource}
              actions={actions}
              openUid={actionMenuUid}
              setOpenUid={setActionMenuUid}
              onAction={onRowAction}
            />
          ),
        },
      };
    });
  }, [baseRows, displayed, actions, actionMenuUid, onRowAction]);

  // Columns including optional actions column
  const uiColumnsWithActions = useMemo<UIColumn[]>(
    () =>
      actions.length === 0
        ? uiColumns
        : [...uiColumns, { key: "_actions", label: "", width: "40px" }],
    [uiColumns, actions],
  );

  // Confirmation dialog
  const confirmMeta = confirmAction
    ? getActionMeta(confirmAction.actionId, confirmAction.resource, kind)
    : null;
  const isDestructive = confirmMeta?.confirm === "destructive";
  const confirmName = confirmAction?.resource.metadata.name ?? "";

  // Empty / loading state rows
  const emptyRows: UIRow[] = loading
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

  const finalRows = loading ? emptyRows : uiRows;

  return (
    <div className="space-y-4">
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
              {itemCountText}
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
                  animation: loading ? "spin 1s linear infinite" : "none",
                }}
                viewBox="0 0 16 16"
                fill="none"
                stroke="currentColor"
                strokeWidth="1.5"
              >
                <path d="M14 8A6 6 0 1 1 8 2" />
                <path d="M14 2v4h-4" />
              </svg>
            </button>
            {createHref && canPerformCheck(rbacValue, kind, "create", ns) && (
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
                  strokeWidth="2.5"
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
      {error && <ErrorBanner message={error} />}

      {/* What a restored view could not honour, and what it changed beyond
          this table. Dismissible, but never auto-cleared: the user decides
          when they have read it. */}
      {restoreWarnings.length > 0 && (
        <div
          data-testid="saved-view-restore-notice"
          style={{
            display: "flex",
            alignItems: "flex-start",
            gap: "10px",
            padding: "8px 10px",
            marginBottom: "8px",
            borderRadius: "9px",
            fontSize: "12px",
            color: "var(--warning)",
            background: "color-mix(in srgb, var(--warning) 10%, transparent)",
            border:
              "1px solid color-mix(in srgb, var(--warning) 40%, transparent)",
          }}
        >
          <ul style={{ margin: 0, paddingLeft: "16px", flex: "1 1 auto" }}>
            {restoreWarnings.map((w) => (
              <li key={w}>{w}</li>
            ))}
          </ul>
          <button
            type="button"
            data-testid="saved-view-restore-notice-dismiss"
            aria-label="Dismiss"
            onClick={() => {
              setRestoreWarnings([]);
            }}
            style={{
              background: "transparent",
              border: "none",
              color: "inherit",
              cursor: "pointer",
              fontSize: "14px",
              lineHeight: 1,
              padding: "0 2px",
            }}
          >
            x
          </button>
        </div>
      )}

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
            value={search}
            onInput={(v) => {
              updateFilters((prev) => ({ ...prev, search: v }));
            }}
            placeholder={`Search ${title.toLowerCase()}...`}
          />
        </div>
        <SavedViews
          resourceKind={kind}
          current={viewState}
          onApply={applyView}
        />
        {showFilterChips && (
          <div style={{ display: "flex", gap: "6px", alignItems: "center" }}>
            {filterChips.map((chip) => {
              const val = chip.toLowerCase();
              const isActive = statusFilter === val;
              return (
                <button
                  key={chip}
                  type="button"
                  onClick={() => setStatusFilter(val)}
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
        {toolbarCount !== null && (
          <span
            data-testid="toolbar-count"
            style={{
              marginLeft: "auto",
              fontSize: "12px",
              color: "var(--text-muted)",
              fontFamily: "var(--font-mono, monospace)",
            }}
          >
            {toolbarCount}
          </span>
        )}
      </div>

      {/* ResourceTable — solid surface (bg-surface), glass rule: data = solid */}
      {finalRows.length === 0 && !loading ? (
        <div
          role="table"
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
      ) : (
        <ResourceTable
          columns={uiColumnsWithActions}
          rows={finalRows}
          chevron
          sortKey={sortKey}
          sortDir={sortDir}
          onSort={handleSort}
          busy={loading}
        />
      )}

      {/* Load More */}
      {continueToken && (
        <div className="flex justify-center">
          <button
            type="button"
            onClick={() => fetchResources(true)}
            disabled={loadingMore}
            style={{
              padding: "8px 20px",
              fontSize: "13px",
              fontWeight: 500,
              color: "var(--text-muted)",
              background: "var(--bg-surface)",
              border: "1px solid var(--border-primary)",
              borderRadius: "9px",
              cursor: loadingMore ? "not-allowed" : "pointer",
              opacity: loadingMore ? 0.5 : 1,
            }}
          >
            {loadingMore ? "Loading..." : "Load more"}
          </button>
        </div>
      )}

      {/* Confirm Dialog */}
      {confirmAction && confirmMeta && (
        <ConfirmDialog
          title={`${confirmMeta.label} ${confirmAction.resource.metadata.name}`}
          message={confirmMeta.confirmMessage}
          confirmLabel={confirmMeta.label}
          danger={confirmMeta.danger}
          typeToConfirm={isDestructive ? confirmName : undefined}
          loading={actionLoading}
          onConfirm={() =>
            runAction(
              confirmAction.actionId,
              confirmAction.resource,
              confirmAction.params,
            )
          }
          onCancel={() => {
            setConfirmAction(null);
          }}
        />
      )}

      {/* Scale Dialog */}
      {scaleTarget && (
        <ScaleDialog
          resourceName={scaleTarget.metadata.name}
          currentReplicas={
            (scaleTarget.spec as { replicas?: number } | undefined)?.replicas
          }
          value={scaleValue}
          onValueChange={(v) => {
            setScaleValue(v);
          }}
          loading={actionLoading}
          onConfirm={() =>
            runAction("scale", scaleTarget, {
              replicas: scaleValue,
            })
          }
          onCancel={() => {
            setScaleTarget(null);
          }}
        />
      )}
    </div>
  );
}
