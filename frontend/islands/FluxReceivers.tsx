import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { apiPost, apiPut } from "@/lib/api.ts";
import { showToast } from "@/islands/ToastProvider.tsx";
import { StatusBadge } from "@/components/ui/NotificationBadges.tsx";
import {
  ActionsDropdown,
  CountBadge,
  NotificationDeleteDialog,
  NotificationEmptyState,
  NotificationFormShell,
  NotificationLoadingSpinner,
  NotificationPageHeader,
  NotificationPagination,
  NotificationSearchBar,
  NotificationUnavailableBanner,
} from "@/components/ui/NotificationShared.tsx";
import { useNotificationCrud } from "@/lib/useNotificationCrud.ts";
import type { NormalizedReceiver } from "@/lib/notification-types.ts";
import {
  FLUX_RESOURCE_KINDS,
  INPUT_CLASS,
  PAGE_SIZE,
} from "@/lib/notification-types.ts";
import { timeAgo } from "@/lib/timeAgo.ts";

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
  const crud = useNotificationCrud<NormalizedReceiver>({
    resourceKind: "receivers",
    apiBasePath: "/v1/gitops/notifications",
    wsTopics: [["flux-receivers-sub", "flux-receivers", ""]],
    extractItems: (data) =>
      Array.isArray(data.receivers)
        ? data.receivers as NormalizedReceiver[]
        : [],
    label: "receiver",
  });

  const form = useSignal<ReceiverForm>({ ...EMPTY_FORM });

  function openEdit(r: NormalizedReceiver) {
    crud.openEdit(r, () => {
      form.value = {
        name: r.name,
        namespace: r.namespace,
        type: r.type,
        resources: r.resources.length > 0
          ? r.resources.map((res) => ({ kind: res.kind, name: res.name }))
          : [{ kind: "GitRepository", name: "*" }],
        secretRef: r.secretRef,
      };
    });
  }

  async function handleFormSubmit() {
    if (crud.formSubmitting.value) return;
    const f = form.value;
    if (!f.name.trim()) {
      crud.formError.value = "Name is required";
      return;
    }
    if (!f.secretRef.trim()) {
      crud.formError.value = "Secret reference is required";
      return;
    }
    crud.formSubmitting.value = true;
    crud.formError.value = null;

    const payload = {
      name: f.name,
      namespace: f.namespace,
      type: f.type,
      resources: f.resources.filter((r) => r.name.trim()),
      secretRef: f.secretRef,
    };

    try {
      if (crud.editingItem.value) {
        await apiPut(
          `/v1/gitops/notifications/receivers/${
            encodeURIComponent(crud.editingItem.value.namespace)
          }/${encodeURIComponent(crud.editingItem.value.name)}`,
          payload,
        );
        showToast("Receiver updated", "success");
      } else {
        await apiPost("/v1/gitops/notifications/receivers", payload);
        showToast("Receiver created", "success");
      }
      crud.showForm.value = false;
      await crud.fetchData();
    } catch (err) {
      crud.formError.value = err instanceof Error
        ? err.message
        : "Failed to save receiver";
    } finally {
      crud.formSubmitting.value = false;
    }
  }

  function updateResource(
    index: number,
    field: "kind" | "name",
    value: string,
  ) {
    const updated = form.value.resources.map((r, i) =>
      i === index ? { ...r, [field]: value } : r
    );
    form.value = { ...form.value, resources: updated };
  }

  function addResource() {
    form.value = {
      ...form.value,
      resources: [
        ...form.value.resources,
        { kind: "GitRepository", name: "*" },
      ],
    };
  }

  function removeResource(index: number) {
    if (form.value.resources.length <= 1) return;
    form.value = {
      ...form.value,
      resources: form.value.resources.filter((_, i) => i !== index),
    };
  }

  /** Copy text to clipboard with toast feedback. */
  function copyToClipboard(text: string) {
    navigator.clipboard.writeText(text).then(
      () => showToast("Copied to clipboard", "success"),
      () => showToast("Failed to copy", "error"),
    );
  }

  if (!IS_BROWSER) return null;

  const notAvailable = !!(crud.status.value && !crud.status.value.available);

  const filtered = crud.items.value.filter((r) => {
    if (!crud.search.value) return true;
    const q = crud.search.value.toLowerCase();
    return (
      r.name.toLowerCase().includes(q) ||
      r.namespace.toLowerCase().includes(q) ||
      r.type.toLowerCase().includes(q) ||
      r.webhookPath.toLowerCase().includes(q)
    );
  });

  const totalPages = Math.ceil(filtered.length / PAGE_SIZE) || 1;
  if (crud.page.value > totalPages) crud.page.value = totalPages;
  const displayed = filtered.slice(
    (crud.page.value - 1) * PAGE_SIZE,
    crud.page.value * PAGE_SIZE,
  );

  return (
    <div class="p-6">
      <NotificationPageHeader
        kind="Receiver"
        description="Flux notification receivers &mdash; webhook endpoints that trigger reconciliation."
        loading={crud.loading.value}
        notAvailable={notAvailable}
        refreshing={crud.refreshing.value}
        onRefresh={crud.handleRefresh}
        onCreate={() =>
          crud.openCreate(() => {
            form.value = {
              ...EMPTY_FORM,
              resources: [{ kind: "GitRepository", name: "*" }],
            };
          })}
      />

      <NotificationUnavailableBanner
        visible={notAvailable && !crud.loading.value}
        resourceLabel="receivers"
      />

      <NotificationSearchBar
        search={crud.search}
        page={crud.page}
        filteredCount={filtered.length}
        totalCount={crud.items.value.length}
        resourceLabel="receivers"
        placeholder="Filter by name, namespace, type..."
      />

      <NotificationLoadingSpinner loading={crud.loading.value} />

      {crud.error.value && (
        <p class="text-sm text-danger py-4">{crud.error.value}</p>
      )}

      {!crud.loading.value && !crud.error.value && filtered.length > 0 && (
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
                      <CountBadge items={r.resources} label="resource" />
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
                      <ActionsDropdown
                        itemKey={key}
                        suspended={r.suspend}
                        openDropdown={crud.openDropdown}
                        onEdit={() => openEdit(r)}
                        onSuspendToggle={() => crud.handleSuspendToggle(r)}
                        onDelete={() => {
                          crud.deleteTarget.value = r;
                        }}
                      />
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}

      <NotificationPagination
        loading={crud.loading.value}
        error={crud.error.value}
        filteredCount={filtered.length}
        page={crud.page}
        totalPages={totalPages}
        resourceLabel="receivers"
      />

      <NotificationEmptyState
        loading={crud.loading.value}
        error={crud.error.value}
        filteredCount={filtered.length}
        totalCount={crud.items.value.length}
        notAvailable={notAvailable}
        kind="Receiver"
        onCreate={() =>
          crud.openCreate(() => {
            form.value = {
              ...EMPTY_FORM,
              resources: [{ kind: "GitRepository", name: "*" }],
            };
          })}
      />

      {crud.showForm.value && (
        <NotificationFormShell
          id="receiver-form"
          title={crud.editingItem.value ? "Edit Receiver" : "Create Receiver"}
          submitting={crud.formSubmitting.value}
          error={crud.formError.value}
          onSubmit={handleFormSubmit}
          onCancel={() => {
            crud.showForm.value = false;
          }}
          wide
        >
          <div>
            <label class="block text-sm text-text-secondary mb-1">Name</label>
            <input
              type="text"
              value={form.value.name}
              disabled={!!crud.editingItem.value}
              onInput={(e) =>
                form.value = {
                  ...form.value,
                  name: (e.target as HTMLInputElement).value,
                }}
              class={INPUT_CLASS}
              placeholder="my-receiver"
            />
          </div>
          <div>
            <label class="block text-sm text-text-secondary mb-1">
              Namespace
            </label>
            <input
              type="text"
              value={form.value.namespace}
              disabled={!!crud.editingItem.value}
              onInput={(e) =>
                form.value = {
                  ...form.value,
                  namespace: (e.target as HTMLInputElement).value,
                }}
              class={INPUT_CLASS}
              placeholder="default"
            />
          </div>
          <div>
            <label class="block text-sm text-text-secondary mb-1">Type</label>
            <select
              value={form.value.type}
              onChange={(e) =>
                form.value = {
                  ...form.value,
                  type: (e.target as HTMLSelectElement).value,
                }}
              class={INPUT_CLASS}
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
              {form.value.resources.map((res, i) => (
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
                    {FLUX_RESOURCE_KINDS.map((k) => (
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
                    disabled={form.value.resources.length <= 1}
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
              value={form.value.secretRef}
              onInput={(e) =>
                form.value = {
                  ...form.value,
                  secretRef: (e.target as HTMLInputElement).value,
                }}
              class={INPUT_CLASS}
              placeholder="webhook-token-secret"
            />
          </div>
        </NotificationFormShell>
      )}

      <NotificationDeleteDialog
        target={crud.deleteTarget.value}
        loading={crud.deleteLoading.value}
        kind="Receiver"
        onConfirm={() => {
          if (crud.deleteTarget.value) {
            crud.handleDelete(crud.deleteTarget.value);
          }
        }}
        onCancel={() => {
          crud.deleteTarget.value = null;
        }}
      />
    </div>
  );
}
