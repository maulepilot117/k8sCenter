import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { apiPost, apiPut } from "@/lib/api.ts";
import { showToast } from "@/islands/ToastProvider.tsx";
import ResourceTable from "@/components/ui/ResourceTable.tsx";
import {
  ProviderTypeBadge,
  StatusBadge,
} from "@/components/ui/NotificationBadges.tsx";
import {
  ActionsDropdown,
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
import type { NormalizedProvider } from "@/lib/notification-types.ts";
import { INPUT_CLASS, PAGE_SIZE } from "@/lib/notification-types.ts";
import { filterByNamespace, selectedNamespace } from "@/lib/namespace.ts";
import { timeAgo } from "@/lib/timeAgo.ts";

/** All Flux notification provider types (alphabetical). */
const PROVIDER_TYPES = [
  "alertmanager",
  "azuredevops",
  "azureeventhub",
  "bitbucket",
  "bitbucketserver",
  "datadog",
  "discord",
  "generic",
  "generic-hmac",
  "gitea",
  "giteapullrequestcomment",
  "github",
  "githubdispatch",
  "githubpullrequestcomment",
  "gitlab",
  "gitlabmergerequestcomment",
  "googlechat",
  "googlepubsub",
  "grafana",
  "lark",
  "matrix",
  "msteams",
  "nats",
  "opsgenie",
  "otel",
  "pagerduty",
  "rocket",
  "sentry",
  "slack",
  "telegram",
  "webex",
  "zulip",
];

interface ProviderForm {
  name: string;
  namespace: string;
  type: string;
  address: string;
  channel: string;
  secretRef: string;
}

const EMPTY_FORM: ProviderForm = {
  name: "",
  namespace: "default",
  type: "slack",
  address: "",
  channel: "",
  secretRef: "",
};

export default function FluxProviders() {
  const crud = useNotificationCrud<NormalizedProvider>({
    resourceKind: "providers",
    apiBasePath: "/v1/gitops/notifications",
    wsTopics: [["flux-providers-sub", "flux-providers", ""]],
    extractItems: (data) =>
      Array.isArray(data.providers)
        ? data.providers as NormalizedProvider[]
        : [],
    label: "provider",
  });

  const form = useSignal<ProviderForm>({ ...EMPTY_FORM });

  function openEdit(p: NormalizedProvider) {
    crud.openEdit(p, () => {
      form.value = {
        name: p.name,
        namespace: p.namespace,
        type: p.type,
        address: p.address,
        channel: p.channel,
        secretRef: p.secretRef,
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
    crud.formSubmitting.value = true;
    crud.formError.value = null;
    try {
      if (crud.editingItem.value) {
        await apiPut(
          `/v1/gitops/notifications/providers/${
            encodeURIComponent(crud.editingItem.value.namespace)
          }/${encodeURIComponent(crud.editingItem.value.name)}`,
          f,
        );
        showToast("Provider updated", "success");
      } else {
        await apiPost("/v1/gitops/notifications/providers", f);
        showToast("Provider created", "success");
      }
      crud.showForm.value = false;
      await crud.fetchData();
    } catch (err) {
      crud.formError.value = err instanceof Error
        ? err.message
        : "Failed to save provider";
    } finally {
      crud.formSubmitting.value = false;
    }
  }

  if (!IS_BROWSER) return null;

  const notAvailable = !!(crud.status.value && !crud.status.value.available);

  const nsByNs = filterByNamespace(crud.items.value, selectedNamespace.value);
  const filtered = nsByNs.filter((p) => {
    if (!crud.search.value) return true;
    const q = crud.search.value.toLowerCase();
    return (
      p.name.toLowerCase().includes(q) ||
      p.namespace.toLowerCase().includes(q) ||
      p.type.toLowerCase().includes(q) ||
      p.channel.toLowerCase().includes(q) ||
      p.address.toLowerCase().includes(q)
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
        kind="Provider"
        description="Flux notification providers &mdash; configure where alerts are sent."
        loading={crud.loading.value}
        notAvailable={notAvailable}
        refreshing={crud.refreshing.value}
        onRefresh={crud.handleRefresh}
        onCreate={() =>
          crud.openCreate(() => {
            form.value = { ...EMPTY_FORM };
          })}
      />

      <NotificationUnavailableBanner
        visible={notAvailable && !crud.loading.value}
        resourceLabel="providers"
      />

      <NotificationSearchBar
        search={crud.search}
        page={crud.page}
        filteredCount={filtered.length}
        totalCount={crud.items.value.length}
        resourceLabel="providers"
        placeholder="Filter by name, namespace, type..."
      />

      <NotificationLoadingSpinner loading={crud.loading.value} />

      {crud.error.value && (
        <p class="text-sm py-4" style={{ color: "var(--error)" }}>
          {crud.error.value}
        </p>
      )}

      {!crud.loading.value && !crud.error.value && filtered.length > 0 && (
        <ResourceTable
          chevron={false}
          columns={[
            { key: "name", label: "Name" },
            { key: "namespace", label: "Namespace", width: "120px" },
            { key: "type", label: "Type", width: "140px" },
            { key: "channel", label: "Channel / Address" },
            { key: "status", label: "Status", width: "110px" },
            { key: "createdAt", label: "Created", width: "100px" },
            { key: "actions", label: "", width: "60px" },
          ]}
          rows={displayed.map((p) => ({
            id: `${p.namespace}/${p.name}`,
            cells: {
              name: (
                <div>
                  <div class="font-medium text-text-primary">{p.name}</div>
                  {p.suspend && (
                    <span class="text-xs" style={{ color: "var(--warning)" }}>
                      suspended
                    </span>
                  )}
                </div>
              ),
              namespace: (
                <span class="text-xs text-text-secondary">{p.namespace}</span>
              ),
              type: <ProviderTypeBadge type={p.type} />,
              channel: (
                <span class="text-xs text-text-secondary truncate block max-w-[240px]">
                  {p.channel || p.address || "-"}
                </span>
              ),
              status: (
                <StatusBadge status={p.suspend ? "suspended" : p.status} />
              ),
              createdAt: (
                <span class="text-xs text-text-muted">
                  {p.createdAt ? timeAgo(p.createdAt) : "-"}
                </span>
              ),
              actions: (
                <ActionsDropdown
                  itemKey={`${p.namespace}/${p.name}`}
                  suspended={p.suspend}
                  openDropdown={crud.openDropdown}
                  onEdit={() => openEdit(p)}
                  onSuspendToggle={() => crud.handleSuspendToggle(p)}
                  onDelete={() => {
                    crud.deleteTarget.value = p;
                  }}
                />
              ),
            },
          }))}
        />
      )}

      <NotificationPagination
        loading={crud.loading.value}
        error={crud.error.value}
        filteredCount={filtered.length}
        page={crud.page}
        totalPages={totalPages}
        resourceLabel="providers"
      />

      <NotificationEmptyState
        loading={crud.loading.value}
        error={crud.error.value}
        filteredCount={filtered.length}
        totalCount={crud.items.value.length}
        notAvailable={notAvailable}
        kind="Provider"
        onCreate={() => crud.openCreate(() => {
          form.value = { ...EMPTY_FORM };
        })}
      />

      {crud.showForm.value && (
        <NotificationFormShell
          id="provider-form"
          title={crud.editingItem.value ? "Edit Provider" : "Create Provider"}
          submitting={crud.formSubmitting.value}
          error={crud.formError.value}
          onSubmit={handleFormSubmit}
          onCancel={() => {
            crud.showForm.value = false;
          }}
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
              placeholder="my-slack-provider"
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
              {PROVIDER_TYPES.map((t) => (
                <option key={t} value={t}>
                  {t}
                </option>
              ))}
            </select>
          </div>
          <div>
            <label class="block text-sm text-text-secondary mb-1">
              Address
            </label>
            <input
              type="text"
              value={form.value.address}
              onInput={(e) =>
                form.value = {
                  ...form.value,
                  address: (e.target as HTMLInputElement).value,
                }}
              class={INPUT_CLASS}
              placeholder="https://hooks.slack.com/..."
            />
          </div>
          <div>
            <label class="block text-sm text-text-secondary mb-1">
              Channel
            </label>
            <input
              type="text"
              value={form.value.channel}
              onInput={(e) =>
                form.value = {
                  ...form.value,
                  channel: (e.target as HTMLInputElement).value,
                }}
              class={INPUT_CLASS}
              placeholder="#alerts"
            />
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
              placeholder="webhook-url-secret"
            />
          </div>
        </NotificationFormShell>
      )}

      <NotificationDeleteDialog
        target={crud.deleteTarget.value}
        loading={crud.deleteLoading.value}
        kind="Provider"
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
