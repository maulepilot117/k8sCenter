import type { Signal } from "@preact/signals";
import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { useEffect } from "preact/hooks";
import { apiDelete, apiGet, apiPost } from "@/lib/api.ts";
import { useWsRefetch } from "@/lib/useWsRefetch.ts";
import { showToast } from "@/islands/ToastProvider.tsx";
import type { NotificationStatus } from "@/lib/notification-types.ts";

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
  status: Signal<NotificationStatus | null>;
  items: Signal<T[]>;
  loading: Signal<boolean>;
  error: Signal<string | null>;
  search: Signal<string>;
  page: Signal<number>;
  refreshing: Signal<boolean>;
  showForm: Signal<boolean>;
  editingItem: Signal<T | null>;
  formSubmitting: Signal<boolean>;
  formError: Signal<string | null>;
  deleteTarget: Signal<T | null>;
  deleteLoading: Signal<boolean>;
  openDropdown: Signal<string | null>;
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
  const status = useSignal<NotificationStatus | null>(null);
  const items = useSignal<T[]>([]);
  const loading = useSignal(true);
  const error = useSignal<string | null>(null);
  const search = useSignal("");
  const page = useSignal(1);
  const refreshing = useSignal(false);

  const showForm = useSignal(false);
  const editingItem = useSignal<T | null>(null);
  const formSubmitting = useSignal(false);
  const formError = useSignal<string | null>(null);

  const deleteTarget = useSignal<T | null>(null);
  const deleteLoading = useSignal(false);

  const openDropdown = useSignal<string | null>(null);

  const basePath = `${config.apiBasePath}/${config.resourceKind}`;

  async function fetchData() {
    try {
      const [statusRes, listRes] = await Promise.all([
        apiGet<NotificationStatus>(`${config.apiBasePath}/status`),
        apiGet<Record<string, unknown>>(basePath),
      ]);
      status.value = statusRes.data;
      items.value = config.extractItems(listRes.data);
      error.value = null;
    } catch {
      error.value = `Failed to load notification ${config.label}s`;
    }
  }

  useEffect(() => {
    if (!IS_BROWSER) return;
    fetchData().then(() => {
      loading.value = false;
    });
  }, []);

  useWsRefetch(fetchData, config.wsTopics, 1000);

  // Close dropdown on outside click
  useEffect(() => {
    if (!IS_BROWSER) return;
    const handler = () => {
      openDropdown.value = null;
    };
    globalThis.addEventListener("click", handler);
    return () => globalThis.removeEventListener("click", handler);
  }, []);

  async function handleRefresh() {
    refreshing.value = true;
    await fetchData();
    refreshing.value = false;
  }

  function openCreate(emptyForm: () => void) {
    editingItem.value = null;
    emptyForm();
    formError.value = null;
    showForm.value = true;
  }

  function openEdit(item: T, formMapper: () => void) {
    editingItem.value = item;
    formMapper();
    formError.value = null;
    showForm.value = true;
  }

  async function handleSuspendToggle(item: T) {
    try {
      await apiPost(
        `${basePath}/${encodeURIComponent(item.namespace)}/${
          encodeURIComponent(item.name)
        }/suspend`,
        { suspend: !item.suspend },
      );
      showToast(
        item.suspend ? `Resumed ${item.name}` : `Suspended ${item.name}`,
        "success",
      );
      await fetchData();
    } catch (err) {
      showToast(
        err instanceof Error ? err.message : "Action failed",
        "error",
      );
    }
  }

  async function handleDelete(item: T) {
    if (deleteLoading.value) return;
    deleteLoading.value = true;
    try {
      await apiDelete(
        `${basePath}/${encodeURIComponent(item.namespace)}/${
          encodeURIComponent(item.name)
        }`,
      );
      showToast(`Deleted ${item.name}`, "success");
      deleteTarget.value = null;
      await fetchData();
    } catch (err) {
      showToast(
        err instanceof Error ? err.message : "Delete failed",
        "error",
      );
    } finally {
      deleteLoading.value = false;
    }
  }

  return {
    status,
    items,
    loading,
    error,
    search,
    page,
    refreshing,
    showForm,
    editingItem,
    formSubmitting,
    formError,
    deleteTarget,
    deleteLoading,
    openDropdown,
    fetchData,
    handleRefresh,
    handleSuspendToggle,
    handleDelete,
    openCreate,
    openEdit,
  };
}
