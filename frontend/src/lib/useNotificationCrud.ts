import { useCallback, useEffect, useState } from "react";
import { apiDelete, apiGet, apiPost } from "@/lib/api.ts";
import type { NotificationStatus } from "@/lib/notification-types.ts";
import { useWsRefetch } from "@/lib/useWsRefetch.ts";
import { showToast } from "@/src/lib/toast-store.ts";
import { useLatest } from "@/src/lib/use-latest.ts";
import { useStateRef } from "@/src/lib/use-state-ref.ts";

/** Base interface for items managed by the notification CRUD hook. */
interface NotificationItem {
  name: string;
  namespace: string;
  suspend: boolean;
}

/** Configuration for the useNotificationCrud hook. */
export interface NotificationCrudConfig<T extends NotificationItem> {
  /** Resource kind for API path (e.g. "providers", "alerts", "receivers"). */
  resourceKind: string;
  /** Base API path (e.g. "/v1/gitops/notifications"). */
  apiBasePath: string;
  /** WebSocket subscription tuples: [id, kind, namespace]. */
  wsTopics: Array<[string, string, string]>;
  /** Extract the item array from the list endpoint response. */
  extractItems: (data: Record<string, unknown>) => T[];
  /** Human-readable singular label (e.g. "provider", "alert", "receiver"). */
  label: string;
}

export interface NotificationCrudResult<T extends NotificationItem> {
  status: NotificationStatus | null;
  items: T[];
  loading: boolean;
  error: string | null;
  search: string;
  setSearch: (next: string) => void;
  page: number;
  setPage: (next: number) => void;
  refreshing: boolean;
  showForm: boolean;
  setShowForm: (next: boolean) => void;
  editingItem: T | null;
  formSubmitting: boolean;
  /** Reads the current in-flight state, for a submit guard. */
  formSubmittingRef: { current: boolean };
  setFormSubmitting: (next: boolean) => void;
  formError: string | null;
  setFormError: (next: string | null) => void;
  deleteTarget: T | null;
  setDeleteTarget: (next: T | null) => void;
  deleteLoading: boolean;
  openDropdown: string | null;
  setOpenDropdown: (next: string | null) => void;
  fetchData: () => Promise<void>;
  handleRefresh: () => Promise<void>;
  handleSuspendToggle: (item: T) => Promise<void>;
  handleDelete: (item: T) => Promise<void>;
  openCreate: (emptyForm: () => void) => void;
  openEdit: (item: T, formMapper: () => void) => void;
}

export function useNotificationCrud<T extends NotificationItem>(
  config: NotificationCrudConfig<T>,
): NotificationCrudResult<T> {
  const [status, setStatus] = useState<NotificationStatus | null>(null);
  const [items, setItems] = useState<T[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [search, setSearch] = useState("");
  const [page, setPage] = useState(1);
  const [refreshing, setRefreshing] = useState(false);

  const [showForm, setShowForm] = useState(false);
  const [editingItem, setEditingItem] = useState<T | null>(null);
  const [formSubmitting, setFormSubmitting, formSubmittingRef] =
    useStateRef(false);
  const [formError, setFormError] = useState<string | null>(null);

  const [deleteTarget, setDeleteTarget] = useState<T | null>(null);
  const [deleteLoading, setDeleteLoading, deleteLoadingRef] =
    useStateRef(false);

  const [openDropdown, setOpenDropdown] = useState<string | null>(null);

  const configRef = useLatest(config);

  // Stable: it reads the caller's latest config through the ref, so the mount
  // fetch and the socket refetch always use the current paths and extractor.
  const fetchData = useCallback(async () => {
    const { apiBasePath, resourceKind, extractItems, label } =
      configRef.current;
    try {
      const [statusRes, listRes] = await Promise.all([
        apiGet<NotificationStatus>(`${apiBasePath}/status`),
        apiGet<Record<string, unknown>>(`${apiBasePath}/${resourceKind}`),
      ]);
      setStatus(statusRes.data);
      setItems(extractItems(listRes.data));
      setError(null);
    } catch {
      setError(`Failed to load notification ${label}s`);
    }
  }, []);

  useEffect(() => {
    fetchData().then(() => {
      setLoading(false);
    });
  }, [fetchData]);

  useWsRefetch(fetchData, config.wsTopics, 1000);

  // Close dropdown on outside click
  useEffect(() => {
    const handler = () => {
      setOpenDropdown(null);
    };
    globalThis.addEventListener("click", handler);
    return () => globalThis.removeEventListener("click", handler);
  }, []);

  async function handleRefresh() {
    setRefreshing(true);
    await fetchData();
    setRefreshing(false);
  }

  function openCreate(emptyForm: () => void) {
    setEditingItem(null);
    emptyForm();
    setFormError(null);
    setShowForm(true);
  }

  function openEdit(item: T, formMapper: () => void) {
    setEditingItem(item);
    formMapper();
    setFormError(null);
    setShowForm(true);
  }

  async function handleSuspendToggle(item: T) {
    const basePath = `${config.apiBasePath}/${config.resourceKind}`;
    try {
      await apiPost(
        `${basePath}/${encodeURIComponent(item.namespace)}/${encodeURIComponent(
          item.name,
        )}/suspend`,
        { suspend: !item.suspend },
      );
      showToast(
        item.suspend ? `Resumed ${item.name}` : `Suspended ${item.name}`,
        "success",
      );
      await fetchData();
    } catch (err) {
      showToast(err instanceof Error ? err.message : "Action failed", "error");
    }
  }

  async function handleDelete(item: T) {
    if (deleteLoadingRef.current) return;
    const basePath = `${config.apiBasePath}/${config.resourceKind}`;
    setDeleteLoading(true);
    try {
      await apiDelete(
        `${basePath}/${encodeURIComponent(item.namespace)}/${encodeURIComponent(
          item.name,
        )}`,
      );
      showToast(`Deleted ${item.name}`, "success");
      setDeleteTarget(null);
      await fetchData();
    } catch (err) {
      showToast(err instanceof Error ? err.message : "Delete failed", "error");
    } finally {
      setDeleteLoading(false);
    }
  }

  return {
    status,
    items,
    loading,
    error,
    search,
    setSearch,
    page,
    setPage,
    refreshing,
    showForm,
    setShowForm,
    editingItem,
    formSubmitting,
    formSubmittingRef,
    setFormSubmitting,
    formError,
    setFormError,
    deleteTarget,
    setDeleteTarget,
    deleteLoading,
    openDropdown,
    setOpenDropdown,
    fetchData,
    handleRefresh,
    handleSuspendToggle,
    handleDelete,
    openCreate,
    openEdit,
  };
}
