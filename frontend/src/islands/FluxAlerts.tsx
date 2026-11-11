import { useState } from "react";
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
import ResourceTable from "@/components/ui/ResourceTable.tsx";
import { apiPost, apiPut } from "@/lib/api.ts";
import type { NormalizedAlert } from "@/lib/notification-types.ts";
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
      Array.isArray(data.alerts) ? (data.alerts as NormalizedAlert[]) : [],
    label: "alert",
  });

  const isClient = useIsClient();
  const namespace = useSignalValue(selectedNamespace);
  const [form, setForm] = useState<AlertForm>({ ...EMPTY_FORM });

  function openEdit(a: NormalizedAlert) {
    crud.openEdit(a, () => {
      setForm({
        name: a.name,
        namespace: a.namespace,
        providerRef: a.providerRef,
        eventSeverity: a.eventSeverity,
        eventSources:
          a.eventSources.length > 0
            ? a.eventSources.map((s) => ({ kind: s.kind, name: s.name }))
            : [{ kind: "Kustomization", name: "*" }],
        inclusionList: a.inclusionList.join(", "),
        exclusionList: a.exclusionList.join(", "),
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
    if (!f.providerRef.trim()) {
      crud.setFormError("Provider reference is required");
      return;
    }
    crud.setFormSubmitting(true);
    crud.setFormError(null);

    const payload = {
      name: f.name,
      namespace: f.namespace,
      providerRef: f.providerRef,
      eventSeverity: f.eventSeverity,
      eventSources: f.eventSources.filter((s) => s.name.trim()),
      inclusionList: f.inclusionList
        ? f.inclusionList
            .split(",")
            .map((s) => s.trim())
            .filter(Boolean)
        : [],
      exclusionList: f.exclusionList
        ? f.exclusionList
            .split(",")
            .map((s) => s.trim())
            .filter(Boolean)
        : [],
    };

    try {
      if (crud.editingItem) {
        await apiPut(
          `/v1/gitops/notifications/alerts/${encodeURIComponent(
            crud.editingItem.namespace,
          )}/${encodeURIComponent(crud.editingItem.name)}`,
          payload,
        );
        showToast("Alert updated", "success");
      } else {
        await apiPost("/v1/gitops/notifications/alerts", payload);
        showToast("Alert created", "success");
      }
      crud.setShowForm(false);
      await crud.fetchData();
    } catch (err) {
      crud.setFormError(
        err instanceof Error ? err.message : "Failed to save alert",
      );
    } finally {
      crud.setFormSubmitting(false);
    }
  }

  function updateSource(index: number, field: "kind" | "name", value: string) {
    const updated = form.eventSources.map((s, i) =>
      i === index ? { ...s, [field]: value } : s,
    );
    setForm({ ...form, eventSources: updated });
  }

  function addSource() {
    setForm({
      ...form,
      eventSources: [
        ...form.eventSources,
        { kind: "Kustomization", name: "*" },
      ],
    });
  }

  function removeSource(index: number) {
    if (form.eventSources.length <= 1) return;
    setForm({
      ...form,
      eventSources: form.eventSources.filter((_, i) => i !== index),
    });
  }

  if (!isClient) return null;

  const notAvailable = !!(crud.status && !crud.status.available);

  const nsByNs = filterByNamespace(crud.items, namespace);
  const filtered = nsByNs.filter((a) => {
    if (!crud.search) return true;
    const q = crud.search.toLowerCase();
    return (
      a.name.toLowerCase().includes(q) ||
      a.namespace.toLowerCase().includes(q) ||
      a.providerRef.toLowerCase().includes(q) ||
      a.eventSeverity.toLowerCase().includes(q)
    );
  });

  const totalPages = Math.ceil(filtered.length / PAGE_SIZE) || 1;
  const page = Math.min(crud.page, totalPages);
  if (crud.page > totalPages) crud.setPage(totalPages);
  const displayed = filtered.slice((page - 1) * PAGE_SIZE, page * PAGE_SIZE);

  return (
    <div className="p-6">
      <NotificationPageHeader
        kind="Alert"
        description="Flux notification alerts &mdash; define forwarding rules from event sources to providers."
        loading={crud.loading}
        notAvailable={notAvailable}
        refreshing={crud.refreshing}
        onRefresh={crud.handleRefresh}
        onCreate={() =>
          crud.openCreate(() => {
            setForm({
              ...EMPTY_FORM,
              eventSources: [{ kind: "Kustomization", name: "*" }],
            });
          })
        }
      />

      <NotificationUnavailableBanner
        visible={notAvailable && !crud.loading}
        resourceLabel="alerts"
      />

      <NotificationSearchBar
        search={crud.search}
        onSearchChange={crud.setSearch}
        onPageChange={crud.setPage}
        filteredCount={filtered.length}
        totalCount={crud.items.length}
        resourceLabel="alerts"
        placeholder="Filter by name, namespace, provider..."
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
            { key: "providerRef", label: "Provider" },
            { key: "eventSeverity", label: "Severity", width: "100px" },
            { key: "eventSources", label: "Sources", width: "90px" },
            { key: "status", label: "Status", width: "110px" },
            { key: "createdAt", label: "Created", width: "100px" },
            { key: "actions", label: "", width: "60px" },
          ]}
          rows={displayed.map((a) => ({
            id: `${a.namespace}/${a.name}`,
            cells: {
              name: (
                <div>
                  <div className="font-medium text-text-primary">{a.name}</div>
                  {a.suspend && (
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
                  {a.namespace}
                </span>
              ),
              providerRef: (
                <span className="text-xs text-text-secondary">
                  {a.providerRef}
                </span>
              ),
              eventSeverity: (
                <SeverityBadge severity={a.eventSeverity || "info"} />
              ),
              eventSources: (
                <CountBadge items={a.eventSources} label="source" />
              ),
              status: (
                <StatusBadge status={a.suspend ? "suspended" : a.status} />
              ),
              createdAt: (
                <span className="text-xs text-text-muted">
                  {a.createdAt ? timeAgo(a.createdAt) : "-"}
                </span>
              ),
              actions: (
                <ActionsDropdown
                  itemKey={`${a.namespace}/${a.name}`}
                  suspended={a.suspend}
                  openDropdown={crud.openDropdown}
                  onOpenDropdownChange={crud.setOpenDropdown}
                  onEdit={() => openEdit(a)}
                  onSuspendToggle={() => crud.handleSuspendToggle(a)}
                  onDelete={() => {
                    crud.setDeleteTarget(a);
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
        resourceLabel="alerts"
      />

      <NotificationEmptyState
        loading={crud.loading}
        error={crud.error}
        filteredCount={filtered.length}
        totalCount={crud.items.length}
        notAvailable={notAvailable}
        kind="Alert"
        onCreate={() =>
          crud.openCreate(() => {
            setForm({
              ...EMPTY_FORM,
              eventSources: [{ kind: "Kustomization", name: "*" }],
            });
          })
        }
      />

      {crud.showForm && (
        <NotificationFormShell
          id="alert-form"
          title={crud.editingItem ? "Edit Alert" : "Create Alert"}
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
              placeholder="my-alert"
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
              Provider Reference
            </label>
            <input
              type="text"
              value={form.providerRef}
              onChange={(e) =>
                setForm({
                  ...form,
                  providerRef: e.currentTarget.value,
                })
              }
              className={INPUT_CLASS}
              placeholder="my-slack-provider"
            />
          </div>
          <div>
            <label className="block text-sm text-text-secondary mb-1">
              Event Severity
            </label>
            <select
              value={form.eventSeverity}
              onChange={(e) =>
                setForm({
                  ...form,
                  eventSeverity: e.currentTarget.value,
                })
              }
              className={INPUT_CLASS}
            >
              <option value="info">info</option>
              <option value="error">error</option>
            </select>
          </div>

          {/* Event sources */}
          <div>
            <label className="block text-sm text-text-secondary mb-1">
              Event Sources
            </label>
            <div className="space-y-2">
              {form.eventSources.map((src, i) => (
                // biome-ignore lint/suspicious/noArrayIndexKey: rows are editable and have no id; the list is keyed by position as before
                <div key={i} className="flex items-center gap-2">
                  <select
                    value={src.kind}
                    onChange={(e) =>
                      updateSource(i, "kind", e.currentTarget.value)
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
                    value={src.name}
                    onChange={(e) =>
                      updateSource(i, "name", e.currentTarget.value)
                    }
                    className="flex-1 rounded-md border border-border-primary bg-surface px-2 py-1.5 text-sm text-text-primary focus:outline-none focus:ring-1 focus:ring-brand"
                    placeholder="* (wildcard)"
                  />
                  <button
                    type="button"
                    onClick={() => removeSource(i)}
                    disabled={form.eventSources.length <= 1}
                    className="rounded px-2 py-1 text-xs font-medium hover:bg-hover disabled:opacity-30"
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
              className="mt-2 text-xs font-medium text-brand hover:underline"
            >
              + Add Source
            </button>
          </div>

          <div>
            <label className="block text-sm text-text-secondary mb-1">
              Inclusion List
            </label>
            <input
              type="text"
              value={form.inclusionList}
              onChange={(e) =>
                setForm({
                  ...form,
                  inclusionList: e.currentTarget.value,
                })
              }
              className={INPUT_CLASS}
              placeholder="Comma-separated regex patterns"
            />
          </div>
          <div>
            <label className="block text-sm text-text-secondary mb-1">
              Exclusion List
            </label>
            <input
              type="text"
              value={form.exclusionList}
              onChange={(e) =>
                setForm({
                  ...form,
                  exclusionList: e.currentTarget.value,
                })
              }
              className={INPUT_CLASS}
              placeholder="Comma-separated regex patterns"
            />
          </div>
        </NotificationFormShell>
      )}

      <NotificationDeleteDialog
        target={crud.deleteTarget}
        loading={crud.deleteLoading}
        kind="Alert"
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
