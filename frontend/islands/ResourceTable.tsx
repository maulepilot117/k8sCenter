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
import { DataTable } from "@/components/ui/DataTable.tsx";
import { SearchBar } from "@/components/ui/SearchBar.tsx";
import type { K8sResource } from "@/lib/k8s-types.ts";

interface ResourceTableProps {
  /** API kind string matching backend route, e.g. "pods", "deployments" */
  kind: string;
  /** Display title for the page header */
  title: string;
  /** Whether this resource is cluster-scoped (no namespace filtering) */
  clusterScoped?: boolean;
  /** Whether to subscribe to WebSocket events (false for secrets) */
  enableWS?: boolean;
}

const PAGE_SIZE = 100;

export default function ResourceTable({
  kind,
  title,
  clusterScoped = false,
  enableWS = true,
}: ResourceTableProps) {
  const items = useSignal<K8sResource[]>([]);
  const loading = useSignal(true);
  const error = useSignal<string | null>(null);
  const search = useSignal("");
  const sortKey = useSignal("name");
  const sortDir = useSignal<"asc" | "desc">("asc");
  const continueToken = useSignal<string | null>(null);
  const totalCount = useSignal<number | null>(null);
  const loadingMore = useSignal(false);

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

  const handleSort = (key: string) => {
    if (sortKey.value === key) {
      sortDir.value = sortDir.value === "asc" ? "desc" : "asc";
    } else {
      sortKey.value = key;
      sortDir.value = "asc";
    }
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

  return (
    <div class="space-y-4">
      {/* Header */}
      <div class="flex items-center justify-between">
        <h1 class="text-xl font-semibold text-slate-900 dark:text-white">
          {title}
        </h1>
        <div class="flex items-center gap-3">
          <span class="text-sm text-slate-500 dark:text-slate-400">
            {itemCountText.value}
          </span>
          <button
            type="button"
            onClick={() => fetchResources()}
            class="rounded-md p-1.5 text-slate-400 hover:bg-slate-100 hover:text-slate-600 dark:hover:bg-slate-700 dark:hover:text-slate-300"
            title="Refresh"
          >
            <svg
              class={`h-4 w-4 ${loading.value ? "animate-spin" : ""}`}
              viewBox="0 0 16 16"
              fill="none"
              stroke="currentColor"
              stroke-width="1.5"
            >
              <path d="M14 8A6 6 0 1 1 8 2" />
              <path d="M14 2v4h-4" />
            </svg>
          </button>
        </div>
      </div>

      {/* Search */}
      <div class="max-w-sm">
        <SearchBar
          value={search.value}
          onInput={(v) => {
            search.value = v;
          }}
          placeholder={`Search ${title.toLowerCase()}...`}
        />
      </div>

      {/* Error state */}
      {error.value && (
        <div class="rounded-md border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-800 dark:bg-red-900/20 dark:text-red-400">
          {error.value}
        </div>
      )}

      {/* Table */}
      <div class="rounded-lg border border-slate-200 bg-white dark:border-slate-700 dark:bg-slate-800">
        <DataTable
          columns={columns}
          data={displayed.value}
          sortKey={sortKey.value}
          sortDir={sortDir.value}
          onSort={handleSort}
          rowKey={(r) => r.metadata.uid}
          onRowClick={handleRowClick}
          emptyMessage={loading.value
            ? "Loading resources..."
            : `No ${title.toLowerCase()} found`}
        />
      </div>

      {/* Load More */}
      {continueToken.value && (
        <div class="flex justify-center">
          <button
            type="button"
            onClick={() => fetchResources(true)}
            disabled={loadingMore.value}
            class="rounded-md border border-slate-300 bg-white px-4 py-2 text-sm font-medium text-slate-700 hover:bg-slate-50 disabled:opacity-50 dark:border-slate-600 dark:bg-slate-800 dark:text-slate-300 dark:hover:bg-slate-700"
          >
            {loadingMore.value ? "Loading..." : "Load More"}
          </button>
        </div>
      )}
    </div>
  );
}
