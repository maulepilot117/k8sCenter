import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { useEffect, useRef } from "preact/hooks";
import { apiDelete, apiGet, apiPost, apiPut } from "@/lib/api.ts";
import { wsStatus } from "@/lib/ws.ts";
import { useWsRefetch } from "@/lib/useWsRefetch.ts";
import { SearchBar } from "@/components/ui/SearchBar.tsx";
import { Spinner } from "@/components/ui/Spinner.tsx";
import { Button } from "@/components/ui/Button.tsx";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog.tsx";
import { showToast } from "@/islands/ToastProvider.tsx";
import { StatusBadge } from "@/components/ui/NotificationBadges.tsx";
import type {
  EventSourceRef,
  NormalizedReceiver,
  NotificationStatus,
} from "@/lib/notification-types.ts";
import { timeAgo } from "@/lib/timeAgo.ts";

const PAGE_SIZE = 100;

/** Receiver types supported by Flux notification-controller. */
const RECEIVER_TYPES = [
  "generic",
  "generic-hmac",
  "github",
  "gitlab",
  "bitbucket",
  "harbor",
  "dockerhub",
  "quay",
  "gcr",
  "nexus",
  "acr",
  "cdevents",
];

/** Flux resource kinds for receiver resource subscriptions. */
const RESOURCE_KINDS = [
  "Kustomization",
  "HelmRelease",
  "GitRepository",
  "HelmRepository",
  "HelmChart",
  "Bucket",
  "OCIRepository",
  "ImageRepository",
  "ImagePolicy",
  "ImageUpdateAutomation",
];

interface ReceiverFormResource {
  kind: string;
  name: string;
}

interface ReceiverForm {
  name: string;
  namespace: string;
  type: string;
  resources: ReceiverFormResource[];
  secretRef: string;
}

const EMPTY_FORM: ReceiverForm = {
  name: "",
  namespace: "default",
  type: "generic",
  resources: [{ kind: "GitRepository", name: "*" }],
  secretRef: "",
};

export default function FluxReceivers() {
  const status = useSignal<NotificationStatus | null>(null);
  const receivers = useSignal<NormalizedReceiver[]>([]);
  const loading = useSignal(true);
  const error = useSignal<string | null>(null);
  const search = useSignal("");
  const page = useSignal(1);
  const refreshing = useSignal(false);

  // Modal state
  const showForm = useSignal(false);
  const editingReceiver = useSignal<NormalizedReceiver | null>(null);
  const form = useSignal<ReceiverForm>({ ...EMPTY_FORM });
  const formSubmitting = useSignal(false);
  const formError = useSignal<string | null>(null);

  // Delete confirmation
  const deleteTarget = useSignal<NormalizedReceiver | null>(null);
  const deleteLoading = useSignal(false);

  // Actions dropdown
  const openDropdown = useSignal<string | null>(null);

  async function fetchReceivers() {
    try {
      const [statusRes, receiversRes] = await Promise.all([
        apiGet<NotificationStatus>("/v1/gitops/notifications/status"),
        apiGet<{ receivers: NormalizedReceiver[] }>(
          "/v1/gitops/notifications/receivers",
        ),
      ]);
      status.value = statusRes.data;
      receivers.value = Array.isArray(receiversRes.data.receivers)
        ? receiversRes.data.receivers
        : [];
      error.value = null;
    } catch {
      error.value = "Failed to load notification receivers";
    }
  }

  useEffect(() => {
    if (!IS_BROWSER) return;
    fetchReceivers().then(() => {
      loading.value = false;
    });
  }, []);

  useWsRefetch(fetchReceivers, [
    ["flux-receivers-sub", "flux-receivers", ""],
  ], 1000);

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
    await fetchReceivers();
    refreshing.value = false;
  }

  function openCreate() {
    editingReceiver.value = null;
    form.value = {
      ...EMPTY_FORM,
      resources: [{ kind: "GitRepository", name: "*" }],
    };
    formError.value = null;
    showForm.value = true;
  }

  function openEdit(r: NormalizedReceiver) {
    editingReceiver.value = r;
    form.value = {
      name: r.name,
      namespace: r.namespace,
      type: r.type,
      resources: r.resources.length > 0
        ? r.resources.map((res) => ({ kind: res.kind, name: res.name }))
        : [{ kind: "GitRepository", name: "*" }],
      secretRef: r.secretRef,
    };
    formError.value = null;
    showForm.value = true;
  }

  async function handleFormSubmit() {
    if (formSubmitting.value) return;
    const f = form.value;
    if (!f.name.trim()) {
      formError.value = "Name is required";
      return;
    }
    if (!f.secretRef.trim()) {
      formError.value = "Secret reference is required";
      return;
    }
    formSubmitting.value = true;
    formError.value = null;

    const payload = {
      name: f.name,
      namespace: f.namespace,
      type: f.type,
      resources: f.resources.filter((r) => r.name.trim()),
      secretRef: f.secretRef,
    };

    try {
      if (editingReceiver.value) {
        await apiPut(
          `/v1/gitops/notifications/receivers/${
            encodeURIComponent(editingReceiver.value.namespace)
          }/${encodeURIComponent(editingReceiver.value.name)}`,
          payload,
        );
        showToast("Receiver updated", "success");
      } else {
        await apiPost("/v1/gitops/notifications/receivers", payload);
        showToast("Receiver created", "success");
      }
      showForm.value = false;
      await fetchReceivers();
    } catch (err) {
      formError.value = err instanceof Error
        ? err.message
        : "Failed to save receiver";
    } finally {
      formSubmitting.value = false;
    }
  }

  async function handleDelete(r: NormalizedReceiver) {
    if (deleteLoading.value) return;
    deleteLoading.value = true;
    try {
      await apiDelete(
        `/v1/gitops/notifications/receivers/${
          encodeURIComponent(r.namespace)
        }/${encodeURIComponent(r.name)}`,
      );
      showToast(`Deleted ${r.name}`, "success");
      deleteTarget.value = null;
      await fetchReceivers();
    } catch (err) {
      showToast(
        err instanceof Error ? err.message : "Delete failed",
        "error",
      );
    } finally {
      deleteLoading.value = false;
    }
  }

  async function handleSuspendToggle(r: NormalizedReceiver) {
    try {
      await apiPost(
        `/v1/gitops/notifications/receivers/${
          encodeURIComponent(r.namespace)
        }/${encodeURIComponent(r.name)}/suspend`,
        { suspend: !r.suspend },
      );
      showToast(
        r.suspend ? `Resumed ${r.name}` : `Suspended ${r.name}`,
        "success",
      );
      await fetchReceivers();
    } catch (err) {
      showToast(
        err instanceof Error ? err.message : "Action failed",
        "error",
      );
    }
  }

  /** Copy text to clipboard with toast feedback. */
  function copyToClipboard(text: string) {
    navigator.clipboard.writeText(text).then(
      () => showToast("Copied to clipboard", "success"),
      () => showToast("Failed to copy", "error"),
    );
  }

  if (!IS_BROWSER) return null;

  const notAvailable = status.value && !status.value.available;

  const filtered = receivers.value.filter((r) => {
    if (!search.value) return true;
    const q = search.value.toLowerCase();
    return (
      r.name.toLowerCase().includes(q) ||
      r.namespace.toLowerCase().includes(q) ||
      r.type.toLowerCase().includes(q) ||
      r.webhookPath.toLowerCase().includes(q)
    );
  });

  const totalPages = Math.ceil(filtered.length / PAGE_SIZE) || 1;
  if (page.value > totalPages) page.value = totalPages;
  const displayed = filtered.slice(
    (page.value - 1) * PAGE_SIZE,
    page.value * PAGE_SIZE,
  );

  return (
    <div class="p-6">
      <div class="flex items-center justify-between mb-1">
        <div class="flex items-center gap-2">
          <h1 class="text-2xl font-bold text-text-primary">Receivers</h1>
          {wsStatus.value === "connected" && (
            <span class="inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-xs font-medium text-success bg-success/10">
              <span class="w-1.5 h-1.5 rounded-full bg-success animate-pulse" />
              Live
            </span>
          )}
        </div>
        <div class="flex items-center gap-2">
          {!loading.value && (
            <>
              <Button
                type="button"
                variant="primary"
                onClick={openCreate}
                disabled={!!notAvailable}
              >
                Create Receiver
              </Button>
              <Button
                type="button"
                variant="ghost"
                onClick={handleRefresh}
                disabled={refreshing.value}
              >
                {refreshing.value ? "Refreshing..." : "Refresh"}
              </Button>
            </>
          )}
        </div>
      </div>
      <p class="text-sm text-text-muted mb-6">
        Flux notification receivers &mdash; webhook endpoints that trigger
        reconciliation.
      </p>

      {/* Unavailable banner */}
      {notAvailable && !loading.value && (
        <div
          class="mb-6 rounded-lg border p-4 bg-bg-elevated"
          style={{ borderColor: "var(--warning)" }}
        >
          <p class="text-sm font-medium" style={{ color: "var(--warning)" }}>
            Flux notification-controller not detected
          </p>
          <p class="text-xs text-text-muted mt-1">
            Install the Flux notification-controller to manage notification
            receivers.{" "}
            <a
              href="https://fluxcd.io/docs/components/notification/"
              target="_blank"
              rel="noopener noreferrer"
              class="text-brand hover:underline"
            >
              Learn more &rarr;
            </a>
          </p>
        </div>
      )}

      {/* Filters */}
      <div class="mb-4 flex flex-wrap items-center gap-4">
        <div class="flex-1 max-w-xs">
          <SearchBar
            value={search.value}
            onInput={(v) => {
              search.value = v;
              page.value = 1;
            }}
            placeholder="Filter by name, namespace, type..."
          />
        </div>
        <span class="text-xs text-text-muted">
          {filtered.length} of {receivers.value.length} receivers
        </span>
      </div>

      {loading.value && (
        <div class="flex justify-center py-12">
          <Spinner class="text-brand" />
        </div>
      )}

      {error.value && <p class="text-sm text-danger py-4">{error.value}</p>}

      {!loading.value && !error.value && filtered.length > 0 && (
        <div class="overflow-x-auto rounded-lg border border-border-primary">
          <table class="w-full text-sm">
            <thead>
              <tr class="border-b border-border-primary bg-surface">
                <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Name
                </th>
                <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Namespace
                </th>
                <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Type
                </th>
                <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Resources
                </th>
                <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Webhook Path
                </th>
                <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Status
                </th>
                <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Created
                </th>
                <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Actions
                </th>
              </tr>
            </thead>
            <tbody class="divide-y divide-border-subtle">
              {displayed.map((r) => {
                const key = `${r.namespace}/${r.name}`;
                return (
                  <tr key={key} class="hover:bg-hover/30">
                    <td class="px-3 py-2">
                      <div class="font-medium text-text-primary">{r.name}</div>
                      {r.suspend && (
                        <span
                          class="text-xs"
                          style={{ color: "var(--warning)" }}
                        >
                          suspended
                        </span>
                      )}
                    </td>
                    <td class="px-3 py-2 text-text-secondary text-xs">
                      {r.namespace}
                    </td>
                    <td class="px-3 py-2">
                      <span class="inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium bg-accent/10 text-accent">
                        {r.type}
                      </span>
                    </td>
                    <td class="px-3 py-2">
                      <ResourceCountBadge resources={r.resources} />
                    </td>
                    <td class="px-3 py-2">
                      {r.webhookPath
                        ? (
                          <div class="flex items-center gap-1.5 max-w-[200px]">
                            <code class="text-xs text-text-secondary truncate">
                              {r.webhookPath}
                            </code>
                            <button
                              type="button"
                              onClick={() => copyToClipboard(r.webhookPath)}
                              class="flex-shrink-0 rounded p-0.5 hover:bg-hover text-text-muted hover:text-text-primary"
                              title="Copy webhook path"
                            >
                              <svg
                                width="14"
                                height="14"
                                viewBox="0 0 24 24"
                                fill="none"
                                stroke="currentColor"
                                stroke-width="2"
                                stroke-linecap="round"
                                stroke-linejoin="round"
                              >
                                <rect
                                  x="9"
                                  y="9"
                                  width="13"
                                  height="13"
                                  rx="2"
                                  ry="2"
                                />
                                <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1" />
                              </svg>
                            </button>
                          </div>
                        )
                        : (
                          <span class="text-xs text-text-muted italic">
                            Pending...
                          </span>
                        )}
                    </td>
                    <td class="px-3 py-2">
                      <StatusBadge
                        status={r.suspend ? "suspended" : r.status}
                      />
                    </td>
                    <td class="px-3 py-2 text-text-muted text-xs">
                      {r.createdAt ? timeAgo(r.createdAt) : "-"}
                    </td>
                    <td class="px-3 py-2">
                      <div class="relative">
                        <button
                          type="button"
                          class="rounded px-2 py-1 text-xs font-medium text-text-secondary hover:bg-hover"
                          onClick={(e) => {
                            e.stopPropagation();
                            openDropdown.value = openDropdown.value === key
                              ? null
                              : key;
                          }}
                        >
                          &hellip;
                        </button>
                        {openDropdown.value === key && (
                          <div
                            class="absolute right-0 z-40 mt-1 w-40 rounded-md border border-border-primary bg-surface shadow-lg"
                            onClick={(e) => e.stopPropagation()}
                          >
                            <button
                              type="button"
                              class="w-full text-left px-3 py-2 text-sm text-text-primary hover:bg-hover"
                              onClick={() => {
                                openDropdown.value = null;
                                openEdit(r);
                              }}
                            >
                              Edit
                            </button>
                            <button
                              type="button"
                              class="w-full text-left px-3 py-2 text-sm text-text-primary hover:bg-hover"
                              onClick={() => {
                                openDropdown.value = null;
                                handleSuspendToggle(r);
                              }}
                            >
                              {r.suspend ? "Resume" : "Suspend"}
                            </button>
                            <button
                              type="button"
                              class="w-full text-left px-3 py-2 text-sm hover:bg-hover"
                              style={{ color: "var(--error)" }}
                              onClick={() => {
                                openDropdown.value = null;
                                deleteTarget.value = r;
                              }}
                            >
                              Delete
                            </button>
                          </div>
                        )}
                      </div>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}

      {/* Pagination */}
      {!loading.value && !error.value && filtered.length > PAGE_SIZE && (
        <div class="mt-4 flex items-center justify-between">
          <p class="text-sm text-text-muted">
            {filtered.length} receivers &middot; Page {page.value} of{" "}
            {totalPages}
          </p>
          <div class="flex gap-2">
            <Button
              type="button"
              variant="ghost"
              onClick={() => {
                page.value--;
              }}
              disabled={page.value <= 1}
            >
              Previous
            </Button>
            <Button
              type="button"
              variant="ghost"
              onClick={() => {
                page.value++;
              }}
              disabled={page.value >= totalPages}
            >
              Next
            </Button>
          </div>
        </div>
      )}

      {/* Empty state */}
      {!loading.value && !error.value && filtered.length === 0 &&
        !notAvailable && (
        <div class="text-center py-12 rounded-lg border border-border-primary bg-bg-elevated">
          <p class="text-text-muted mb-4">
            {receivers.value.length === 0
              ? "No notification receivers configured."
              : "No receivers match your filters."}
          </p>
          {receivers.value.length === 0 && (
            <Button type="button" variant="primary" onClick={openCreate}>
              Create Receiver
            </Button>
          )}
        </div>
      )}

      {/* Create / Edit modal */}
      {showForm.value && (
        <ReceiverFormModal
          form={form.value}
          isEdit={!!editingReceiver.value}
          submitting={formSubmitting.value}
          error={formError.value}
          onInput={(f) => {
            form.value = f;
          }}
          onSubmit={handleFormSubmit}
          onCancel={() => {
            showForm.value = false;
          }}
        />
      )}

      {/* Delete confirmation */}
      {deleteTarget.value && (
        <ConfirmDialog
          title={`Delete ${deleteTarget.value.name}`}
          message={`This will permanently delete the notification receiver "${deleteTarget.value.name}" in namespace "${deleteTarget.value.namespace}".`}
          confirmLabel="Delete"
          danger
          loading={deleteLoading.value}
          onConfirm={() => {
            if (deleteTarget.value) handleDelete(deleteTarget.value);
          }}
          onCancel={() => {
            deleteTarget.value = null;
          }}
        />
      )}
    </div>
  );
}

/** Badge showing the count of resources with tooltip listing them. */
function ResourceCountBadge({ resources }: { resources: EventSourceRef[] }) {
  const count = resources.length;
  if (count === 0) return <span class="text-xs text-text-muted">-</span>;

  const tooltip = resources
    .map((r) => `${r.kind}/${r.name}`)
    .join("\n");

  return (
    <span
      class="inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium bg-accent/10 text-accent cursor-default"
      title={tooltip}
    >
      {count} {count === 1 ? "resource" : "resources"}
    </span>
  );
}

/** Modal form for creating/editing a receiver. */
function ReceiverFormModal({
  form,
  isEdit,
  submitting,
  error,
  onInput,
  onSubmit,
  onCancel,
}: {
  form: ReceiverForm;
  isEdit: boolean;
  submitting: boolean;
  error: string | null;
  onInput: (f: ReceiverForm) => void;
  onSubmit: () => void;
  onCancel: () => void;
}) {
  const nameRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if (e.key === "Escape") onCancel();
    };
    globalThis.addEventListener("keydown", handler);
    nameRef.current?.focus();
    return () => globalThis.removeEventListener("keydown", handler);
  }, [onCancel]);

  const inputClass =
    "w-full rounded-md border border-border-primary bg-surface px-3 py-2 text-sm text-text-primary focus:outline-none focus:ring-1 focus:ring-brand";

  function updateResource(
    index: number,
    field: "kind" | "name",
    value: string,
  ) {
    const updated = form.resources.map((r, i) =>
      i === index ? { ...r, [field]: value } : r
    );
    onInput({ ...form, resources: updated });
  }

  function addResource() {
    onInput({
      ...form,
      resources: [...form.resources, { kind: "GitRepository", name: "*" }],
    });
  }

  function removeResource(index: number) {
    if (form.resources.length <= 1) return;
    onInput({
      ...form,
      resources: form.resources.filter((_, i) => i !== index),
    });
  }

  return (
    <div
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/50"
      onClick={onCancel}
    >
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby="receiver-form-title"
        class="w-full max-w-lg rounded-lg bg-surface p-6 shadow-xl max-h-[90vh] overflow-y-auto"
        onClick={(e) => e.stopPropagation()}
      >
        <h3
          id="receiver-form-title"
          class="text-lg font-semibold text-text-primary mb-4"
        >
          {isEdit ? "Edit Receiver" : "Create Receiver"}
        </h3>

        {error && <p class="text-sm text-danger mb-3">{error}</p>}

        <div class="space-y-3">
          <div>
            <label class="block text-sm text-text-secondary mb-1">Name</label>
            <input
              ref={nameRef}
              type="text"
              value={form.name}
              disabled={isEdit}
              onInput={(e) =>
                onInput({
                  ...form,
                  name: (e.target as HTMLInputElement).value,
                })}
              class={inputClass}
              placeholder="my-receiver"
            />
          </div>
          <div>
            <label class="block text-sm text-text-secondary mb-1">
              Namespace
            </label>
            <input
              type="text"
              value={form.namespace}
              disabled={isEdit}
              onInput={(e) =>
                onInput({
                  ...form,
                  namespace: (e.target as HTMLInputElement).value,
                })}
              class={inputClass}
              placeholder="default"
            />
          </div>
          <div>
            <label class="block text-sm text-text-secondary mb-1">Type</label>
            <select
              value={form.type}
              onChange={(e) =>
                onInput({
                  ...form,
                  type: (e.target as HTMLSelectElement).value,
                })}
              class={inputClass}
            >
              {RECEIVER_TYPES.map((t) => (
                <option key={t} value={t}>
                  {t}
                </option>
              ))}
            </select>
          </div>

          {/* Resources */}
          <div>
            <label class="block text-sm text-text-secondary mb-1">
              Resources
            </label>
            <div class="space-y-2">
              {form.resources.map((res, i) => (
                <div key={i} class="flex items-center gap-2">
                  <select
                    value={res.kind}
                    onChange={(e) =>
                      updateResource(
                        i,
                        "kind",
                        (e.target as HTMLSelectElement).value,
                      )}
                    class="flex-1 rounded-md border border-border-primary bg-surface px-2 py-1.5 text-sm text-text-primary focus:outline-none focus:ring-1 focus:ring-brand"
                  >
                    {RESOURCE_KINDS.map((k) => (
                      <option key={k} value={k}>{k}</option>
                    ))}
                  </select>
                  <input
                    type="text"
                    value={res.name}
                    onInput={(e) =>
                      updateResource(
                        i,
                        "name",
                        (e.target as HTMLInputElement).value,
                      )}
                    class="flex-1 rounded-md border border-border-primary bg-surface px-2 py-1.5 text-sm text-text-primary focus:outline-none focus:ring-1 focus:ring-brand"
                    placeholder="* (wildcard)"
                  />
                  <button
                    type="button"
                    onClick={() =>
                      removeResource(i)}
                    disabled={form.resources.length <= 1}
                    class="rounded px-2 py-1 text-xs font-medium hover:bg-hover disabled:opacity-30"
                    style={{ color: "var(--error)" }}
                    title="Remove resource"
                  >
                    &times;
                  </button>
                </div>
              ))}
            </div>
            <button
              type="button"
              onClick={addResource}
              class="mt-2 text-xs font-medium text-brand hover:underline"
            >
              + Add Resource
            </button>
          </div>

          <div>
            <label class="block text-sm text-text-secondary mb-1">
              Secret Ref
            </label>
            <input
              type="text"
              value={form.secretRef}
              onInput={(e) =>
                onInput({
                  ...form,
                  secretRef: (e.target as HTMLInputElement).value,
                })}
              class={inputClass}
              placeholder="webhook-token-secret"
            />
          </div>
        </div>

        <div class="mt-6 flex justify-end gap-3">
          <button
            type="button"
            onClick={onCancel}
            class="rounded-md border border-border-primary px-4 py-2 text-sm font-medium text-text-secondary hover:bg-hover"
          >
            Cancel
          </button>
          <button
            type="button"
            disabled={submitting}
            onClick={onSubmit}
            class="rounded-md px-4 py-2 text-sm font-medium text-white disabled:opacity-50 bg-brand hover:bg-brand/90"
          >
            {submitting ? "..." : isEdit ? "Update" : "Create"}
          </button>
        </div>
      </div>
    </div>
  );
}
