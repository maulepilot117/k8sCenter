/** @jsxImportSource react */
import { useState } from "react";
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
import ResourceTable from "@/components/ui/ResourceTable.tsx";
import { apiPost, apiPut } from "@/lib/api.ts";
import type { NormalizedProvider } from "@/lib/notification-types.ts";
import { INPUT_CLASS, PAGE_SIZE } from "@/lib/notification-types.ts";
import { timeAgo } from "@/lib/timeAgo.ts";
import { filterByNamespace, selectedNamespace } from "@/src/lib/namespace.ts";
import { showToast } from "@/src/lib/toast-store.ts";
import { useIsClient } from "@/src/lib/use-is-client.ts";
import { useSignalValue } from "@/src/lib/use-signal-value.ts";
import { useNotificationCrud } from "@/src/lib/useNotificationCrud.ts";

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
        ? (data.providers as NormalizedProvider[])
        : [],
    label: "provider",
  });

  const isClient = useIsClient();
  const namespace = useSignalValue(selectedNamespace);
  const [form, setForm] = useState<ProviderForm>({ ...EMPTY_FORM });

  function openEdit(p: NormalizedProvider) {
    crud.openEdit(p, () => {
      setForm({
        name: p.name,
        namespace: p.namespace,
        type: p.type,
        address: p.address,
        channel: p.channel,
        secretRef: p.secretRef,
      });
    });
  }

  async function handleFormSubmit() {
    if (crud.formSubmittingRef.current) return;
    const f = form;
    if (!f.name.trim()) {
      crud.setFormError("Name is required");
      return;
    }
    crud.setFormSubmitting(true);
    crud.setFormError(null);
    try {
      if (crud.editingItem) {
        await apiPut(
          `/v1/gitops/notifications/providers/${encodeURIComponent(
            crud.editingItem.namespace,
          )}/${encodeURIComponent(crud.editingItem.name)}`,
          f,
        );
        showToast("Provider updated", "success");
      } else {
        await apiPost("/v1/gitops/notifications/providers", f);
        showToast("Provider created", "success");
      }
      crud.setShowForm(false);
      await crud.fetchData();
    } catch (err) {
      crud.setFormError(
        err instanceof Error ? err.message : "Failed to save provider",
      );
    } finally {
      crud.setFormSubmitting(false);
    }
  }

  if (!isClient) return null;

  const notAvailable = !!(crud.status && !crud.status.available);

  const nsByNs = filterByNamespace(crud.items, namespace);
  const filtered = nsByNs.filter((p) => {
    if (!crud.search) return true;
    const q = crud.search.toLowerCase();
    return (
      p.name.toLowerCase().includes(q) ||
      p.namespace.toLowerCase().includes(q) ||
      p.type.toLowerCase().includes(q) ||
      p.channel.toLowerCase().includes(q) ||
      p.address.toLowerCase().includes(q)
    );
  });

  const totalPages = Math.ceil(filtered.length / PAGE_SIZE) || 1;
  const page = Math.min(crud.page, totalPages);
  if (crud.page > totalPages) crud.setPage(totalPages);
  const displayed = filtered.slice((page - 1) * PAGE_SIZE, page * PAGE_SIZE);

  return (
    <div className="p-6">
      <NotificationPageHeader
        kind="Provider"
        description="Flux notification providers &mdash; configure where alerts are sent."
        loading={crud.loading}
        notAvailable={notAvailable}
        refreshing={crud.refreshing}
        onRefresh={crud.handleRefresh}
        onCreate={() =>
          crud.openCreate(() => {
            setForm({ ...EMPTY_FORM });
          })
        }
      />

      <NotificationUnavailableBanner
        visible={notAvailable && !crud.loading}
        resourceLabel="providers"
      />

      <NotificationSearchBar
        search={crud.search}
        onSearchChange={crud.setSearch}
        onPageChange={crud.setPage}
        filteredCount={filtered.length}
        totalCount={crud.items.length}
        resourceLabel="providers"
        placeholder="Filter by name, namespace, type..."
      />

      <NotificationLoadingSpinner loading={crud.loading} />

      {crud.error && (
        <p className="text-sm py-4" style={{ color: "var(--error)" }}>
          {crud.error}
        </p>
      )}

      {!crud.loading && !crud.error && filtered.length > 0 && (
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
                  <div className="font-medium text-text-primary">{p.name}</div>
                  {p.suspend && (
                    <span
                      className="text-xs"
                      style={{ color: "var(--warning)" }}
                    >
                      suspended
                    </span>
                  )}
                </div>
              ),
              namespace: (
                <span className="text-xs text-text-secondary">
                  {p.namespace}
                </span>
              ),
              type: <ProviderTypeBadge type={p.type} />,
              channel: (
                <span className="text-xs text-text-secondary truncate block max-w-[240px]">
                  {p.channel || p.address || "-"}
                </span>
              ),
              status: (
                <StatusBadge status={p.suspend ? "suspended" : p.status} />
              ),
              createdAt: (
                <span className="text-xs text-text-muted">
                  {p.createdAt ? timeAgo(p.createdAt) : "-"}
                </span>
              ),
              actions: (
                <ActionsDropdown
                  itemKey={`${p.namespace}/${p.name}`}
                  suspended={p.suspend}
                  openDropdown={crud.openDropdown}
                  onOpenDropdownChange={crud.setOpenDropdown}
                  onEdit={() => openEdit(p)}
                  onSuspendToggle={() => crud.handleSuspendToggle(p)}
                  onDelete={() => {
                    crud.setDeleteTarget(p);
                  }}
                />
              ),
            },
          }))}
        />
      )}

      <NotificationPagination
        loading={crud.loading}
        error={crud.error}
        filteredCount={filtered.length}
        page={page}
        onPageChange={crud.setPage}
        totalPages={totalPages}
        resourceLabel="providers"
      />

      <NotificationEmptyState
        loading={crud.loading}
        error={crud.error}
        filteredCount={filtered.length}
        totalCount={crud.items.length}
        notAvailable={notAvailable}
        kind="Provider"
        onCreate={() =>
          crud.openCreate(() => {
            setForm({ ...EMPTY_FORM });
          })
        }
      />

      {crud.showForm && (
        <NotificationFormShell
          id="provider-form"
          title={crud.editingItem ? "Edit Provider" : "Create Provider"}
          submitting={crud.formSubmitting}
          error={crud.formError}
          onSubmit={handleFormSubmit}
          onCancel={() => {
            crud.setShowForm(false);
          }}
        >
          <div>
            <label className="block text-sm text-text-secondary mb-1">
              Name
            </label>
            <input
              type="text"
              value={form.name}
              disabled={!!crud.editingItem}
              onChange={(e) =>
                setForm({
                  ...form,
                  name: e.currentTarget.value,
                })
              }
              className={INPUT_CLASS}
              placeholder="my-slack-provider"
            />
          </div>
          <div>
            <label className="block text-sm text-text-secondary mb-1">
              Namespace
            </label>
            <input
              type="text"
              value={form.namespace}
              disabled={!!crud.editingItem}
              onChange={(e) =>
                setForm({
                  ...form,
                  namespace: e.currentTarget.value,
                })
              }
              className={INPUT_CLASS}
              placeholder="default"
            />
          </div>
          <div>
            <label className="block text-sm text-text-secondary mb-1">
              Type
            </label>
            <select
              value={form.type}
              onChange={(e) =>
                setForm({
                  ...form,
                  type: e.currentTarget.value,
                })
              }
              className={INPUT_CLASS}
            >
              {PROVIDER_TYPES.map((t) => (
                <option key={t} value={t}>
                  {t}
                </option>
              ))}
            </select>
          </div>
          <div>
            <label className="block text-sm text-text-secondary mb-1">
              Address
            </label>
            <input
              type="text"
              value={form.address}
              onChange={(e) =>
                setForm({
                  ...form,
                  address: e.currentTarget.value,
                })
              }
              className={INPUT_CLASS}
              placeholder="https://hooks.slack.com/..."
            />
          </div>
          <div>
            <label className="block text-sm text-text-secondary mb-1">
              Channel
            </label>
            <input
              type="text"
              value={form.channel}
              onChange={(e) =>
                setForm({
                  ...form,
                  channel: e.currentTarget.value,
                })
              }
              className={INPUT_CLASS}
              placeholder="#alerts"
            />
          </div>
          <div>
            <label className="block text-sm text-text-secondary mb-1">
              Secret Ref
            </label>
            <input
              type="text"
              value={form.secretRef}
              onChange={(e) =>
                setForm({
                  ...form,
                  secretRef: e.currentTarget.value,
                })
              }
              className={INPUT_CLASS}
              placeholder="webhook-url-secret"
            />
          </div>
        </NotificationFormShell>
      )}

      <NotificationDeleteDialog
        target={crud.deleteTarget}
        loading={crud.deleteLoading}
        kind="Provider"
        onConfirm={() => {
          if (crud.deleteTarget) {
            crud.handleDelete(crud.deleteTarget);
          }
        }}
        onCancel={() => {
          crud.setDeleteTarget(null);
        }}
      />
    </div>
  );
}
