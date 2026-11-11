import { useState } from "react";
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
import ResourceTable from "@/components/ui/ResourceTable.tsx";
import { apiPost, apiPut } from "@/lib/api.ts";
import type { NormalizedReceiver } from "@/lib/notification-types.ts";
import {
  FLUX_RESOURCE_KINDS,
  INPUT_CLASS,
  PAGE_SIZE,
} from "@/lib/notification-types.ts";
import { timeAgo } from "@/lib/timeAgo.ts";
import { filterByNamespace, selectedNamespace } from "@/src/lib/namespace.ts";
import { showToast } from "@/src/lib/toast-store.ts";
import { useIsClient } from "@/src/lib/use-is-client.ts";
import { useSignalValue } from "@/src/lib/use-signal-value.ts";
import { useNotificationCrud } from "@/src/lib/useNotificationCrud.ts";

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
        ? (data.receivers as NormalizedReceiver[])
        : [],
    label: "receiver",
  });

  const isClient = useIsClient();
  const namespace = useSignalValue(selectedNamespace);
  const [form, setForm] = useState<ReceiverForm>({ ...EMPTY_FORM });

  function openEdit(r: NormalizedReceiver) {
    crud.openEdit(r, () => {
      setForm({
        name: r.name,
        namespace: r.namespace,
        type: r.type,
        resources:
          r.resources.length > 0
            ? r.resources.map((res) => ({ kind: res.kind, name: res.name }))
            : [{ kind: "GitRepository", name: "*" }],
        secretRef: r.secretRef,
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
    if (!f.secretRef.trim()) {
      crud.setFormError("Secret reference is required");
      return;
    }
    crud.setFormSubmitting(true);
    crud.setFormError(null);

    const payload = {
      name: f.name,
      namespace: f.namespace,
      type: f.type,
      resources: f.resources.filter((r) => r.name.trim()),
      secretRef: f.secretRef,
    };

    try {
      if (crud.editingItem) {
        await apiPut(
          `/v1/gitops/notifications/receivers/${encodeURIComponent(
            crud.editingItem.namespace,
          )}/${encodeURIComponent(crud.editingItem.name)}`,
          payload,
        );
        showToast("Receiver updated", "success");
      } else {
        await apiPost("/v1/gitops/notifications/receivers", payload);
        showToast("Receiver created", "success");
      }
      crud.setShowForm(false);
      await crud.fetchData();
    } catch (err) {
      crud.setFormError(
        err instanceof Error ? err.message : "Failed to save receiver",
      );
    } finally {
      crud.setFormSubmitting(false);
    }
  }

  function updateResource(
    index: number,
    field: "kind" | "name",
    value: string,
  ) {
    const updated = form.resources.map((r, i) =>
      i === index ? { ...r, [field]: value } : r,
    );
    setForm({ ...form, resources: updated });
  }

  function addResource() {
    setForm({
      ...form,
      resources: [...form.resources, { kind: "GitRepository", name: "*" }],
    });
  }

  function removeResource(index: number) {
    if (form.resources.length <= 1) return;
    setForm({
      ...form,
      resources: form.resources.filter((_, i) => i !== index),
    });
  }

  /** Copy text to clipboard with toast feedback. */
  function copyToClipboard(text: string) {
    navigator.clipboard.writeText(text).then(
      () => showToast("Copied to clipboard", "success"),
      () => showToast("Failed to copy", "error"),
    );
  }

  if (!isClient) return null;

  const notAvailable = !!(crud.status && !crud.status.available);

  const nsByNs = filterByNamespace(crud.items, namespace);
  const filtered = nsByNs.filter((r) => {
    if (!crud.search) return true;
    const q = crud.search.toLowerCase();
    return (
      r.name.toLowerCase().includes(q) ||
      r.namespace.toLowerCase().includes(q) ||
      r.type.toLowerCase().includes(q) ||
      r.webhookPath.toLowerCase().includes(q)
    );
  });

  const totalPages = Math.ceil(filtered.length / PAGE_SIZE) || 1;
  const page = Math.min(crud.page, totalPages);
  if (crud.page > totalPages) crud.setPage(totalPages);
  const displayed = filtered.slice((page - 1) * PAGE_SIZE, page * PAGE_SIZE);

  return (
    <div className="p-6">
      <NotificationPageHeader
        kind="Receiver"
        description="Flux notification receivers &mdash; webhook endpoints that trigger reconciliation."
        loading={crud.loading}
        notAvailable={notAvailable}
        refreshing={crud.refreshing}
        onRefresh={crud.handleRefresh}
        onCreate={() =>
          crud.openCreate(() => {
            setForm({
              ...EMPTY_FORM,
              resources: [{ kind: "GitRepository", name: "*" }],
            });
          })
        }
      />

      <NotificationUnavailableBanner
        visible={notAvailable && !crud.loading}
        resourceLabel="receivers"
      />

      <NotificationSearchBar
        search={crud.search}
        onSearchChange={crud.setSearch}
        onPageChange={crud.setPage}
        filteredCount={filtered.length}
        totalCount={crud.items.length}
        resourceLabel="receivers"
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
            { key: "type", label: "Type", width: "120px" },
            { key: "resources", label: "Resources", width: "90px" },
            { key: "webhookPath", label: "Webhook Path" },
            { key: "status", label: "Status", width: "110px" },
            { key: "createdAt", label: "Created", width: "100px" },
            { key: "actions", label: "", width: "60px" },
          ]}
          rows={displayed.map((r) => ({
            id: `${r.namespace}/${r.name}`,
            cells: {
              name: (
                <div>
                  <div className="font-medium text-text-primary">{r.name}</div>
                  {r.suspend && (
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
                  {r.namespace}
                </span>
              ),
              type: (
                <span className="inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium bg-accent/10 text-accent">
                  {r.type}
                </span>
              ),
              resources: <CountBadge items={r.resources} label="resource" />,
              webhookPath: r.webhookPath ? (
                <div className="flex items-center gap-1.5 max-w-[200px]">
                  <code className="text-xs text-text-secondary truncate">
                    {r.webhookPath}
                  </code>
                  <button
                    type="button"
                    onClick={() => copyToClipboard(r.webhookPath)}
                    className="flex-shrink-0 rounded p-0.5 hover:bg-hover text-text-muted hover:text-text-primary"
                    title="Copy webhook path"
                  >
                    <svg
                      width="14"
                      height="14"
                      viewBox="0 0 24 24"
                      fill="none"
                      stroke="currentColor"
                      strokeWidth="2"
                      strokeLinecap="round"
                      strokeLinejoin="round"
                    >
                      <rect x="9" y="9" width="13" height="13" rx="2" ry="2" />
                      <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1" />
                    </svg>
                  </button>
                </div>
              ) : (
                <span className="text-xs text-text-muted italic">
                  Pending...
                </span>
              ),
              status: (
                <StatusBadge status={r.suspend ? "suspended" : r.status} />
              ),
              createdAt: (
                <span className="text-xs text-text-muted">
                  {r.createdAt ? timeAgo(r.createdAt) : "-"}
                </span>
              ),
              actions: (
                <ActionsDropdown
                  itemKey={`${r.namespace}/${r.name}`}
                  suspended={r.suspend}
                  openDropdown={crud.openDropdown}
                  onOpenDropdownChange={crud.setOpenDropdown}
                  onEdit={() => openEdit(r)}
                  onSuspendToggle={() => crud.handleSuspendToggle(r)}
                  onDelete={() => {
                    crud.setDeleteTarget(r);
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
        resourceLabel="receivers"
      />

      <NotificationEmptyState
        loading={crud.loading}
        error={crud.error}
        filteredCount={filtered.length}
        totalCount={crud.items.length}
        notAvailable={notAvailable}
        kind="Receiver"
        onCreate={() =>
          crud.openCreate(() => {
            setForm({
              ...EMPTY_FORM,
              resources: [{ kind: "GitRepository", name: "*" }],
            });
          })
        }
      />

      {crud.showForm && (
        <NotificationFormShell
          id="receiver-form"
          title={crud.editingItem ? "Edit Receiver" : "Create Receiver"}
          submitting={crud.formSubmitting}
          error={crud.formError}
          onSubmit={handleFormSubmit}
          onCancel={() => {
            crud.setShowForm(false);
          }}
          wide
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
              placeholder="my-receiver"
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
              {RECEIVER_TYPES.map((t) => (
                <option key={t} value={t}>
                  {t}
                </option>
              ))}
            </select>
          </div>

          {/* Resources */}
          <div>
            <label className="block text-sm text-text-secondary mb-1">
              Resources
            </label>
            <div className="space-y-2">
              {form.resources.map((res, i) => (
                // biome-ignore lint/suspicious/noArrayIndexKey: rows are editable and have no id; the list is keyed by position as before
                <div key={i} className="flex items-center gap-2">
                  <select
                    value={res.kind}
                    onChange={(e) =>
                      updateResource(i, "kind", e.currentTarget.value)
                    }
                    className="flex-1 rounded-md border border-border-primary bg-surface px-2 py-1.5 text-sm text-text-primary focus:outline-none focus:ring-1 focus:ring-brand"
                  >
                    {FLUX_RESOURCE_KINDS.map((k) => (
                      <option key={k} value={k}>
                        {k}
                      </option>
                    ))}
                  </select>
                  <input
                    type="text"
                    value={res.name}
                    onChange={(e) =>
                      updateResource(i, "name", e.currentTarget.value)
                    }
                    className="flex-1 rounded-md border border-border-primary bg-surface px-2 py-1.5 text-sm text-text-primary focus:outline-none focus:ring-1 focus:ring-brand"
                    placeholder="* (wildcard)"
                  />
                  <button
                    type="button"
                    onClick={() => removeResource(i)}
                    disabled={form.resources.length <= 1}
                    className="rounded px-2 py-1 text-xs font-medium hover:bg-hover disabled:opacity-30"
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
              className="mt-2 text-xs font-medium text-brand hover:underline"
            >
              + Add Resource
            </button>
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
              placeholder="webhook-token-secret"
            />
          </div>
        </NotificationFormShell>
      )}

      <NotificationDeleteDialog
        target={crud.deleteTarget}
        loading={crud.deleteLoading}
        kind="Receiver"
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
