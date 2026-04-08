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
import {
  SeverityBadge,
  StatusBadge,
} from "@/components/ui/NotificationBadges.tsx";
import type {
  EventSourceRef,
  NormalizedAlert,
  NotificationStatus,
} from "@/lib/notification-types.ts";
import { timeAgo } from "@/lib/timeAgo.ts";

const PAGE_SIZE = 100;

/** Flux event source kinds available for alert subscriptions. */
const EVENT_SOURCE_KINDS = [
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
  const status = useSignal<NotificationStatus | null>(null);
  const alerts = useSignal<NormalizedAlert[]>([]);
  const loading = useSignal(true);
  const error = useSignal<string | null>(null);
  const search = useSignal("");
  const page = useSignal(1);
  const refreshing = useSignal(false);

  // Modal state
  const showForm = useSignal(false);
  const editingAlert = useSignal<NormalizedAlert | null>(null);
  const form = useSignal<AlertForm>({ ...EMPTY_FORM });
  const formSubmitting = useSignal(false);
  const formError = useSignal<string | null>(null);

  // Delete confirmation
  const deleteTarget = useSignal<NormalizedAlert | null>(null);
  const deleteLoading = useSignal(false);

  // Actions dropdown
  const openDropdown = useSignal<string | null>(null);

  async function fetchAlerts() {
    try {
      const [statusRes, alertsRes] = await Promise.all([
        apiGet<NotificationStatus>("/v1/gitops/notifications/status"),
        apiGet<{ alerts: NormalizedAlert[] }>(
          "/v1/gitops/notifications/alerts",
        ),
      ]);
      status.value = statusRes.data;
      alerts.value = Array.isArray(alertsRes.data.alerts)
        ? alertsRes.data.alerts
        : [];
      error.value = null;
    } catch {
      error.value = "Failed to load notification alerts";
    }
  }

  useEffect(() => {
    if (!IS_BROWSER) return;
    fetchAlerts().then(() => {
      loading.value = false;
    });
  }, []);

  useWsRefetch(fetchAlerts, [
    ["flux-alerts-sub", "flux-alerts", ""],
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
    await fetchAlerts();
    refreshing.value = false;
  }

  function openCreate() {
    editingAlert.value = null;
    form.value = { ...EMPTY_FORM, eventSources: [{ kind: "Kustomization", name: "*" }] };
    formError.value = null;
    showForm.value = true;
  }

  function openEdit(a: NormalizedAlert) {
    editingAlert.value = a;
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
    if (!f.providerRef.trim()) {
      formError.value = "Provider reference is required";
      return;
    }
    formSubmitting.value = true;
    formError.value = null;

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
      if (editingAlert.value) {
        await apiPut(
          `/v1/gitops/notifications/alerts/${
            encodeURIComponent(editingAlert.value.namespace)
          }/${encodeURIComponent(editingAlert.value.name)}`,
          payload,
        );
        showToast("Alert updated", "success");
      } else {
        await apiPost("/v1/gitops/notifications/alerts", payload);
        showToast("Alert created", "success");
      }
      showForm.value = false;
      await fetchAlerts();
    } catch (err) {
      formError.value = err instanceof Error
        ? err.message
        : "Failed to save alert";
    } finally {
      formSubmitting.value = false;
    }
  }

  async function handleSuspendToggle(a: NormalizedAlert) {
    try {
      await apiPost(
        `/v1/gitops/notifications/alerts/${encodeURIComponent(a.namespace)}/${
          encodeURIComponent(a.name)
        }/suspend`,
        { suspend: !a.suspend },
      );
      showToast(
        a.suspend ? `Resumed ${a.name}` : `Suspended ${a.name}`,
        "success",
      );
      await fetchAlerts();
    } catch (err) {
      showToast(
        err instanceof Error ? err.message : "Action failed",
        "error",
      );
    }
  }

  async function handleDelete(a: NormalizedAlert) {
    if (deleteLoading.value) return;
    deleteLoading.value = true;
    try {
      await apiDelete(
        `/v1/gitops/notifications/alerts/${encodeURIComponent(a.namespace)}/${
          encodeURIComponent(a.name)
        }`,
      );
      showToast(`Deleted ${a.name}`, "success");
      deleteTarget.value = null;
      await fetchAlerts();
    } catch (err) {
      showToast(
        err instanceof Error ? err.message : "Delete failed",
        "error",
      );
    } finally {
      deleteLoading.value = false;
    }
  }

  if (!IS_BROWSER) return null;

  const notAvailable = status.value && !status.value.available;

  const filtered = alerts.value.filter((a) => {
    if (!search.value) return true;
    const q = search.value.toLowerCase();
    return (
      a.name.toLowerCase().includes(q) ||
      a.namespace.toLowerCase().includes(q) ||
      a.providerRef.toLowerCase().includes(q) ||
      a.eventSeverity.toLowerCase().includes(q)
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
          <h1 class="text-2xl font-bold text-text-primary">Alerts</h1>
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
                Create Alert
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
        Flux notification alerts &mdash; define forwarding rules from event
        sources to providers.
      </p>

      {/* Unavailable banner */}
      {notAvailable && !loading.value && (
        <div class="mb-6 rounded-lg border p-4 bg-bg-elevated" style={{ borderColor: "var(--warning)" }}>
          <p class="text-sm font-medium" style={{ color: "var(--warning)" }}>
            Flux notification-controller not detected
          </p>
          <p class="text-xs text-text-muted mt-1">
            Install the Flux notification-controller to manage notification
            alerts.{" "}
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
            placeholder="Filter by name, namespace, provider..."
          />
        </div>
        <span class="text-xs text-text-muted">
          {filtered.length} of {alerts.value.length} alerts
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
                        <span class="text-xs" style={{ color: "var(--warning)" }}>
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
                      <SourceCountBadge sources={a.eventSources} />
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
                      <div class="relative">
                        <button
                          type="button"
                          class="rounded px-2 py-1 text-xs font-medium text-text-secondary hover:bg-hover"
                          onClick={(e) => {
                            e.stopPropagation();
                            openDropdown.value =
                              openDropdown.value === key ? null : key;
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
                                openEdit(a);
                              }}
                            >
                              Edit
                            </button>
                            <button
                              type="button"
                              class="w-full text-left px-3 py-2 text-sm text-text-primary hover:bg-hover"
                              onClick={() => {
                                openDropdown.value = null;
                                handleSuspendToggle(a);
                              }}
                            >
                              {a.suspend ? "Resume" : "Suspend"}
                            </button>
                            <button
                              type="button"
                              class="w-full text-left px-3 py-2 text-sm hover:bg-hover"
                              style={{ color: "var(--error)" }}
                              onClick={() => {
                                openDropdown.value = null;
                                deleteTarget.value = a;
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
            {filtered.length} alerts &middot; Page {page.value} of{" "}
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
            {alerts.value.length === 0
              ? "No notification alerts configured."
              : "No alerts match your filters."}
          </p>
          {alerts.value.length === 0 && (
            <Button type="button" variant="primary" onClick={openCreate}>
              Create Alert
            </Button>
          )}
        </div>
      )}

      {/* Create / Edit modal */}
      {showForm.value && (
        <AlertFormModal
          form={form.value}
          isEdit={!!editingAlert.value}
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
          message={`This will permanently delete the notification alert "${deleteTarget.value.name}" in namespace "${deleteTarget.value.namespace}".`}
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

/** Badge showing the count of event sources with tooltip listing them. */
function SourceCountBadge({ sources }: { sources: EventSourceRef[] }) {
  const count = sources.length;
  if (count === 0) return <span class="text-xs text-text-muted">-</span>;

  const tooltip = sources
    .map((s) => `${s.kind}/${s.name}`)
    .join("\n");

  return (
    <span
      class="inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium bg-accent/10 text-accent cursor-default"
      title={tooltip}
    >
      {count} {count === 1 ? "source" : "sources"}
    </span>
  );
}

/** Modal form for creating/editing an alert. */
function AlertFormModal({
  form,
  isEdit,
  submitting,
  error,
  onInput,
  onSubmit,
  onCancel,
}: {
  form: AlertForm;
  isEdit: boolean;
  submitting: boolean;
  error: string | null;
  onInput: (f: AlertForm) => void;
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

  function updateSource(index: number, field: "kind" | "name", value: string) {
    const updated = form.eventSources.map((s, i) =>
      i === index ? { ...s, [field]: value } : s
    );
    onInput({ ...form, eventSources: updated });
  }

  function addSource() {
    onInput({
      ...form,
      eventSources: [...form.eventSources, { kind: "Kustomization", name: "*" }],
    });
  }

  function removeSource(index: number) {
    if (form.eventSources.length <= 1) return;
    onInput({
      ...form,
      eventSources: form.eventSources.filter((_, i) => i !== index),
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
        aria-labelledby="alert-form-title"
        class="w-full max-w-lg rounded-lg bg-surface p-6 shadow-xl max-h-[90vh] overflow-y-auto"
        onClick={(e) => e.stopPropagation()}
      >
        <h3 id="alert-form-title" class="text-lg font-semibold text-text-primary mb-4">
          {isEdit ? "Edit Alert" : "Create Alert"}
        </h3>

        {error && (
          <p class="text-sm text-danger mb-3">{error}</p>
        )}

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
              placeholder="my-alert"
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
            <label class="block text-sm text-text-secondary mb-1">
              Provider Reference
            </label>
            <input
              type="text"
              value={form.providerRef}
              onInput={(e) =>
                onInput({
                  ...form,
                  providerRef: (e.target as HTMLInputElement).value,
                })}
              class={inputClass}
              placeholder="my-slack-provider"
            />
          </div>
          <div>
            <label class="block text-sm text-text-secondary mb-1">
              Event Severity
            </label>
            <select
              value={form.eventSeverity}
              onChange={(e) =>
                onInput({
                  ...form,
                  eventSeverity: (e.target as HTMLSelectElement).value,
                })}
              class={inputClass}
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
              {form.eventSources.map((src, i) => (
                <div key={i} class="flex items-center gap-2">
                  <select
                    value={src.kind}
                    onChange={(e) =>
                      updateSource(i, "kind", (e.target as HTMLSelectElement).value)}
                    class="flex-1 rounded-md border border-border-primary bg-surface px-2 py-1.5 text-sm text-text-primary focus:outline-none focus:ring-1 focus:ring-brand"
                  >
                    {EVENT_SOURCE_KINDS.map((k) => (
                      <option key={k} value={k}>{k}</option>
                    ))}
                  </select>
                  <input
                    type="text"
                    value={src.name}
                    onInput={(e) =>
                      updateSource(i, "name", (e.target as HTMLInputElement).value)}
                    class="flex-1 rounded-md border border-border-primary bg-surface px-2 py-1.5 text-sm text-text-primary focus:outline-none focus:ring-1 focus:ring-brand"
                    placeholder="* (wildcard)"
                  />
                  <button
                    type="button"
                    onClick={() => removeSource(i)}
                    disabled={form.eventSources.length <= 1}
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
              value={form.inclusionList}
              onInput={(e) =>
                onInput({
                  ...form,
                  inclusionList: (e.target as HTMLInputElement).value,
                })}
              class={inputClass}
              placeholder="Comma-separated regex patterns"
            />
          </div>
          <div>
            <label class="block text-sm text-text-secondary mb-1">
              Exclusion List
            </label>
            <input
              type="text"
              value={form.exclusionList}
              onInput={(e) =>
                onInput({
                  ...form,
                  exclusionList: (e.target as HTMLInputElement).value,
                })}
              class={inputClass}
              placeholder="Comma-separated regex patterns"
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
            {submitting
              ? "..."
              : isEdit
              ? "Update"
              : "Create"}
          </button>
        </div>
      </div>
    </div>
  );
}
