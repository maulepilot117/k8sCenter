import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { apiPost, apiPut } from "@/lib/api.ts";
import { showToast } from "@/islands/ToastProvider.tsx";
import {
  SeverityBadge,
  StatusBadge,
} from "@/components/ui/NotificationBadges.tsx";
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
import type { NormalizedAlert } from "@/lib/notification-types.ts";
import {
  FLUX_RESOURCE_KINDS,
  INPUT_CLASS,
  PAGE_SIZE,
} from "@/lib/notification-types.ts";
import { timeAgo } from "@/lib/timeAgo.ts";

interface AlertFormSource {
  kind: string;
  name: string;
}

interface AlertForm {
  name: string;
  namespace: string;
  providerRef: string;
  eventSeverity: string;
  eventSources: AlertFormSource[];
  inclusionList: string;
  exclusionList: string;
}

const EMPTY_FORM: AlertForm = {
  name: "",
  namespace: "default",
  providerRef: "",
  eventSeverity: "info",
  eventSources: [{ kind: "Kustomization", name: "*" }],
  inclusionList: "",
  exclusionList: "",
};

export default function FluxAlerts() {
  const crud = useNotificationCrud<NormalizedAlert>({
    resourceKind: "alerts",
    apiBasePath: "/v1/gitops/notifications",
    wsTopics: [["flux-alerts-sub", "flux-alerts", ""]],
    extractItems: (data) =>
      Array.isArray(data.alerts) ? data.alerts as NormalizedAlert[] : [],
    label: "alert",
  });

  const form = useSignal<AlertForm>({ ...EMPTY_FORM });

  function openEdit(a: NormalizedAlert) {
    crud.openEdit(a, () => {
      form.value = {
        name: a.name,
        namespace: a.namespace,
        providerRef: a.providerRef,
        eventSeverity: a.eventSeverity,
        eventSources: a.eventSources.length > 0
          ? a.eventSources.map((s) => ({ kind: s.kind, name: s.name }))
          : [{ kind: "Kustomization", name: "*" }],
        inclusionList: a.inclusionList.join(", "),
        exclusionList: a.exclusionList.join(", "),
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
    if (!f.providerRef.trim()) {
      crud.formError.value = "Provider reference is required";
      return;
    }
    crud.formSubmitting.value = true;
    crud.formError.value = null;

    const payload = {
      name: f.name,
      namespace: f.namespace,
      providerRef: f.providerRef,
      eventSeverity: f.eventSeverity,
      eventSources: f.eventSources.filter((s) => s.name.trim()),
      inclusionList: f.inclusionList
        ? f.inclusionList.split(",").map((s) => s.trim()).filter(Boolean)
        : [],
      exclusionList: f.exclusionList
        ? f.exclusionList.split(",").map((s) => s.trim()).filter(Boolean)
        : [],
    };

    try {
      if (crud.editingItem.value) {
        await apiPut(
          `/v1/gitops/notifications/alerts/${
            encodeURIComponent(crud.editingItem.value.namespace)
          }/${encodeURIComponent(crud.editingItem.value.name)}`,
          payload,
        );
        showToast("Alert updated", "success");
      } else {
        await apiPost("/v1/gitops/notifications/alerts", payload);
        showToast("Alert created", "success");
      }
      crud.showForm.value = false;
      await crud.fetchData();
    } catch (err) {
      crud.formError.value = err instanceof Error
        ? err.message
        : "Failed to save alert";
    } finally {
      crud.formSubmitting.value = false;
    }
  }

  function updateSource(index: number, field: "kind" | "name", value: string) {
    const updated = form.value.eventSources.map((s, i) =>
      i === index ? { ...s, [field]: value } : s
    );
    form.value = { ...form.value, eventSources: updated };
  }

  function addSource() {
    form.value = {
      ...form.value,
      eventSources: [
        ...form.value.eventSources,
        { kind: "Kustomization", name: "*" },
      ],
    };
  }

  function removeSource(index: number) {
    if (form.value.eventSources.length <= 1) return;
    form.value = {
      ...form.value,
      eventSources: form.value.eventSources.filter((_, i) => i !== index),
    };
  }

  if (!IS_BROWSER) return null;

  const notAvailable = !!(crud.status.value && !crud.status.value.available);

  const filtered = crud.items.value.filter((a) => {
    if (!crud.search.value) return true;
    const q = crud.search.value.toLowerCase();
    return (
      a.name.toLowerCase().includes(q) ||
      a.namespace.toLowerCase().includes(q) ||
      a.providerRef.toLowerCase().includes(q) ||
      a.eventSeverity.toLowerCase().includes(q)
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
        kind="Alert"
        description="Flux notification alerts &mdash; define forwarding rules from event sources to providers."
        loading={crud.loading.value}
        notAvailable={notAvailable}
        refreshing={crud.refreshing.value}
        onRefresh={crud.handleRefresh}
        onCreate={() =>
          crud.openCreate(() => {
            form.value = {
              ...EMPTY_FORM,
              eventSources: [{ kind: "Kustomization", name: "*" }],
            };
          })}
      />

      <NotificationUnavailableBanner
        visible={notAvailable && !crud.loading.value}
        resourceLabel="alerts"
      />

      <NotificationSearchBar
        search={crud.search}
        page={crud.page}
        filteredCount={filtered.length}
        totalCount={crud.items.value.length}
        resourceLabel="alerts"
        placeholder="Filter by name, namespace, provider..."
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
                  Provider
                </th>
                <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Severity
                </th>
                <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Sources
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
              {displayed.map((a) => {
                const key = `${a.namespace}/${a.name}`;
                return (
                  <tr key={key} class="hover:bg-hover/30">
                    <td class="px-3 py-2">
                      <div class="font-medium text-text-primary">{a.name}</div>
                      {a.suspend && (
                        <span
                          class="text-xs"
                          style={{ color: "var(--warning)" }}
                        >
                          suspended
                        </span>
                      )}
                    </td>
                    <td class="px-3 py-2 text-text-secondary text-xs">
                      {a.namespace}
                    </td>
                    <td class="px-3 py-2 text-text-secondary text-xs">
                      {a.providerRef}
                    </td>
                    <td class="px-3 py-2">
                      <SeverityBadge severity={a.eventSeverity || "info"} />
                    </td>
                    <td class="px-3 py-2">
                      <CountBadge items={a.eventSources} label="source" />
                    </td>
                    <td class="px-3 py-2">
                      <StatusBadge
                        status={a.suspend ? "suspended" : a.status}
                      />
                    </td>
                    <td class="px-3 py-2 text-text-muted text-xs">
                      {a.createdAt ? timeAgo(a.createdAt) : "-"}
                    </td>
                    <td class="px-3 py-2">
                      <ActionsDropdown
                        itemKey={key}
                        suspended={a.suspend}
                        openDropdown={crud.openDropdown}
                        onEdit={() => openEdit(a)}
                        onSuspendToggle={() => crud.handleSuspendToggle(a)}
                        onDelete={() => {
                          crud.deleteTarget.value = a;
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
        resourceLabel="alerts"
      />

      <NotificationEmptyState
        loading={crud.loading.value}
        error={crud.error.value}
        filteredCount={filtered.length}
        totalCount={crud.items.value.length}
        notAvailable={notAvailable}
        kind="Alert"
        onCreate={() =>
          crud.openCreate(() => {
            form.value = {
              ...EMPTY_FORM,
              eventSources: [{ kind: "Kustomization", name: "*" }],
            };
          })}
      />

      {crud.showForm.value && (
        <NotificationFormShell
          id="alert-form"
          title={crud.editingItem.value ? "Edit Alert" : "Create Alert"}
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
              placeholder="my-alert"
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
            <label class="block text-sm text-text-secondary mb-1">
              Provider Reference
            </label>
            <input
              type="text"
              value={form.value.providerRef}
              onInput={(e) =>
                form.value = {
                  ...form.value,
                  providerRef: (e.target as HTMLInputElement).value,
                }}
              class={INPUT_CLASS}
              placeholder="my-slack-provider"
            />
          </div>
          <div>
            <label class="block text-sm text-text-secondary mb-1">
              Event Severity
            </label>
            <select
              value={form.value.eventSeverity}
              onChange={(e) =>
                form.value = {
                  ...form.value,
                  eventSeverity: (e.target as HTMLSelectElement).value,
                }}
              class={INPUT_CLASS}
            >
              <option value="info">info</option>
              <option value="error">error</option>
            </select>
          </div>

          {/* Event sources */}
          <div>
            <label class="block text-sm text-text-secondary mb-1">
              Event Sources
            </label>
            <div class="space-y-2">
              {form.value.eventSources.map((src, i) => (
                <div key={i} class="flex items-center gap-2">
                  <select
                    value={src.kind}
                    onChange={(e) =>
                      updateSource(
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
                    value={src.name}
                    onInput={(e) =>
                      updateSource(
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
                      removeSource(i)}
                    disabled={form.value.eventSources.length <= 1}
                    class="rounded px-2 py-1 text-xs font-medium hover:bg-hover disabled:opacity-30"
                    style={{ color: "var(--error)" }}
                    title="Remove source"
                  >
                    &times;
                  </button>
                </div>
              ))}
            </div>
            <button
              type="button"
              onClick={addSource}
              class="mt-2 text-xs font-medium text-brand hover:underline"
            >
              + Add Source
            </button>
          </div>

          <div>
            <label class="block text-sm text-text-secondary mb-1">
              Inclusion List
            </label>
            <input
              type="text"
              value={form.value.inclusionList}
              onInput={(e) =>
                form.value = {
                  ...form.value,
                  inclusionList: (e.target as HTMLInputElement).value,
                }}
              class={INPUT_CLASS}
              placeholder="Comma-separated regex patterns"
            />
          </div>
          <div>
            <label class="block text-sm text-text-secondary mb-1">
              Exclusion List
            </label>
            <input
              type="text"
              value={form.value.exclusionList}
              onInput={(e) =>
                form.value = {
                  ...form.value,
                  exclusionList: (e.target as HTMLInputElement).value,
                }}
              class={INPUT_CLASS}
              placeholder="Comma-separated regex patterns"
            />
          </div>
        </NotificationFormShell>
      )}

      <NotificationDeleteDialog
        target={crud.deleteTarget.value}
        loading={crud.deleteLoading.value}
        kind="Alert"
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
