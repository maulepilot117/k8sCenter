import { useSignal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { notifApi } from "@/lib/api.ts";
import type {
  NotifChannel,
  NotifChannelInput,
  NotifChannelType,
} from "@/lib/notif-center-types.ts";
import { showToast } from "@/islands/ToastProvider.tsx";

const CHANNEL_TYPES: { value: NotifChannelType; label: string }[] = [
  { value: "slack", label: "Slack" },
  { value: "email", label: "Email" },
  { value: "webhook", label: "Webhook" },
];

const TYPE_ICONS: Record<NotifChannelType, string> = {
  slack: "#",
  email: "\u2709",
  webhook: "\u21C4",
};

function formatDate(iso: string): string {
  try {
    return new Date(iso).toLocaleDateString(undefined, {
      year: "numeric",
      month: "short",
      day: "numeric",
    });
  } catch {
    return iso;
  }
}

export default function NotificationChannels() {
  const channels = useSignal<NotifChannel[]>([]);
  const loading = useSignal(true);
  const error = useSignal<string | null>(null);

  // Modal state
  const modalOpen = useSignal(false);
  const editingId = useSignal<string | null>(null);
  const saving = useSignal(false);

  // Form state
  const formName = useSignal("");
  const formType = useSignal<NotifChannelType>("slack");
  const formWebhookUrl = useSignal("");
  const formRecipients = useSignal("");
  const formSchedule = useSignal("daily");
  const formUrl = useSignal("");
  const formSecret = useSignal("");

  // Delete confirmation
  const deleteTarget = useSignal<NotifChannel | null>(null);
  const deleteConfirmText = useSignal("");

  // Test result: channelId -> { ok, timestamp }
  const testResults = useSignal<
    Record<string, { ok: boolean; ts: number }>
  >({});

  async function fetchChannels() {
    loading.value = true;
    error.value = null;
    try {
      const res = await notifApi.listChannels();
      channels.value = res.data ?? [];
    } catch (err) {
      error.value = err instanceof Error
        ? err.message
        : "Failed to load channels";
    } finally {
      loading.value = false;
    }
  }

  useEffect(() => {
    if (!IS_BROWSER) return;
    fetchChannels();
  }, []);

  function resetForm() {
    formName.value = "";
    formType.value = "slack";
    formWebhookUrl.value = "";
    formRecipients.value = "";
    formSchedule.value = "daily";
    formUrl.value = "";
    formSecret.value = "";
    editingId.value = null;
  }

  function openCreate() {
    resetForm();
    modalOpen.value = true;
  }

  function openEdit(ch: NotifChannel) {
    editingId.value = ch.id;
    formName.value = ch.name;
    formType.value = ch.type;
    // Pre-fill config (values come masked from API)
    const cfg = ch.config ?? {};
    formWebhookUrl.value = (cfg.webhookUrl as string) ?? "";
    formRecipients.value = Array.isArray(cfg.recipients)
      ? (cfg.recipients as string[]).join(", ")
      : (cfg.recipients as string) ?? "";
    formSchedule.value = (cfg.schedule as string) ?? "daily";
    formUrl.value = (cfg.url as string) ?? "";
    formSecret.value = (cfg.secret as string) ?? "";
    modalOpen.value = true;
  }

  function buildInput(): NotifChannelInput {
    const base = { name: formName.value.trim(), type: formType.value };
    switch (formType.value) {
      case "slack":
        return { ...base, config: { webhookUrl: formWebhookUrl.value.trim() } };
      case "email":
        return {
          ...base,
          config: {
            recipients: formRecipients.value.split(",").map((r) => r.trim())
              .filter(Boolean),
            schedule: formSchedule.value,
          },
        };
      case "webhook":
        return {
          ...base,
          config: {
            url: formUrl.value.trim(),
            ...(formSecret.value.trim()
              ? { secret: formSecret.value.trim() }
              : {}),
          },
        };
    }
  }

  async function handleSave() {
    if (!formName.value.trim()) {
      showToast("Name is required", "error");
      return;
    }
    saving.value = true;
    try {
      const input = buildInput();
      if (editingId.value) {
        await notifApi.updateChannel(editingId.value, input);
        showToast("Channel updated", "success");
      } else {
        await notifApi.createChannel(input);
        showToast("Channel created", "success");
      }
      modalOpen.value = false;
      resetForm();
      await fetchChannels();
    } catch (err) {
      showToast(
        err instanceof Error ? err.message : "Save failed",
        "error",
      );
    } finally {
      saving.value = false;
    }
  }

  async function handleTest(ch: NotifChannel) {
    try {
      await notifApi.testChannel(ch.id);
      testResults.value = {
        ...testResults.value,
        [ch.id]: { ok: true, ts: Date.now() },
      };
    } catch {
      testResults.value = {
        ...testResults.value,
        [ch.id]: { ok: false, ts: Date.now() },
      };
    }
    // Clear after 3 seconds
    setTimeout(() => {
      const cur = { ...testResults.value };
      delete cur[ch.id];
      testResults.value = cur;
    }, 3000);
  }

  async function handleDelete() {
    const target = deleteTarget.value;
    if (!target) return;
    saving.value = true;
    try {
      await notifApi.deleteChannel(target.id);
      showToast(`Deleted "${target.name}"`, "success");
      deleteTarget.value = null;
      deleteConfirmText.value = "";
      await fetchChannels();
    } catch (err) {
      showToast(
        err instanceof Error ? err.message : "Delete failed",
        "error",
      );
    } finally {
      saving.value = false;
    }
  }

  // --- Render helpers ---

  const inputClass =
    "w-full rounded-md border border-border-primary bg-surface px-3 py-2 text-sm text-text-primary";

  function renderConfigForm() {
    switch (formType.value) {
      case "slack":
        return (
          <div class="mt-3">
            <label class="block text-xs font-medium text-text-primary mb-1">
              Webhook URL
            </label>
            <input
              type="url"
              class={inputClass}
              placeholder="https://hooks.slack.com/services/..."
              value={formWebhookUrl.value}
              onInput={(e) =>
                formWebhookUrl.value = (e.target as HTMLInputElement).value}
            />
          </div>
        );
      case "email":
        return (
          <div class="mt-3 space-y-3">
            <div>
              <label class="block text-xs font-medium text-text-primary mb-1">
                Recipients (comma-separated)
              </label>
              <input
                type="text"
                class={inputClass}
                placeholder="admin@example.com, ops@example.com"
                value={formRecipients.value}
                onInput={(e) =>
                  formRecipients.value = (e.target as HTMLInputElement).value}
              />
            </div>
            <div>
              <label class="block text-xs font-medium text-text-primary mb-1">
                Schedule
              </label>
              <select
                class={inputClass}
                value={formSchedule.value}
                onChange={(e) =>
                  formSchedule.value = (e.target as HTMLSelectElement).value}
              >
                <option value="daily">Daily digest</option>
                <option value="weekly">Weekly digest</option>
              </select>
            </div>
          </div>
        );
      case "webhook":
        return (
          <div class="mt-3 space-y-3">
            <div>
              <label class="block text-xs font-medium text-text-primary mb-1">
                URL
              </label>
              <input
                type="url"
                class={inputClass}
                placeholder="https://example.com/webhook"
                value={formUrl.value}
                onInput={(e) =>
                  formUrl.value = (e.target as HTMLInputElement).value}
              />
            </div>
            <div>
              <label class="block text-xs font-medium text-text-primary mb-1">
                Secret (optional)
              </label>
              <input
                type="password"
                class={inputClass}
                placeholder="HMAC signing secret"
                value={formSecret.value}
                onInput={(e) =>
                  formSecret.value = (e.target as HTMLInputElement).value}
              />
            </div>
          </div>
        );
    }
  }

  function renderTestResult(ch: NotifChannel) {
    const result = testResults.value[ch.id];
    if (!result) return null;
    return result.ok
      ? (
        <span class="ml-2 text-xs" style={{ color: "var(--success)" }}>
          {"\u2713"}
        </span>
      )
      : (
        <span class="ml-2 text-xs" style={{ color: "var(--danger)" }}>
          {"\u2717"}
        </span>
      );
  }

  // --- Main render ---

  if (loading.value && channels.value.length === 0) {
    return (
      <div class="flex items-center justify-center py-12">
        <div
          class="h-6 w-6 animate-spin rounded-full border-2 border-current border-t-transparent"
          style={{ color: "var(--accent)" }}
        />
      </div>
    );
  }

  if (error.value) {
    return (
      <div
        class="rounded-md px-4 py-3 text-sm"
        style={{
          backgroundColor: "color-mix(in srgb, var(--danger) 10%, transparent)",
          color: "var(--danger)",
        }}
      >
        {error.value}
      </div>
    );
  }

  return (
    <div>
      {/* Header */}
      <div class="flex items-center justify-between mb-4">
        <h2
          class="text-lg font-semibold"
          style={{ color: "var(--text-primary)" }}
        >
          Notification Channels
        </h2>
        <button
          type="button"
          class="rounded-md px-3 py-1.5 text-sm font-medium text-white"
          style={{ backgroundColor: "var(--accent)" }}
          onClick={openCreate}
        >
          Create Channel
        </button>
      </div>

      {/* Table */}
      {channels.value.length === 0
        ? (
          <p
            class="py-8 text-center text-sm"
            style={{ color: "var(--text-primary)", opacity: 0.6 }}
          >
            No channels configured yet.
          </p>
        )
        : (
          <div
            class="overflow-x-auto rounded-lg border"
            style={{
              borderColor: "var(--border-primary)",
              backgroundColor: "var(--bg-elevated)",
            }}
          >
            <table
              class="w-full text-sm"
              style={{ color: "var(--text-primary)" }}
            >
              <thead>
                <tr
                  class="text-left text-xs uppercase tracking-wider"
                  style={{
                    borderBottom: "1px solid var(--border-primary)",
                    color: "var(--text-primary)",
                    opacity: 0.7,
                  }}
                >
                  <th class="px-4 py-3 font-medium">Type</th>
                  <th class="px-4 py-3 font-medium">Name</th>
                  <th class="px-4 py-3 font-medium">Status</th>
                  <th class="px-4 py-3 font-medium">Config</th>
                  <th class="px-4 py-3 font-medium">Created</th>
                  <th class="px-4 py-3 font-medium text-right">Actions</th>
                </tr>
              </thead>
              <tbody>
                {channels.value.map((ch) => (
                  <tr
                    key={ch.id}
                    class="border-t"
                    style={{ borderColor: "var(--border-primary)" }}
                  >
                    <td class="px-4 py-3">
                      <span
                        class="inline-flex items-center gap-1.5 rounded-full px-2 py-0.5 text-xs font-medium"
                        style={{
                          backgroundColor:
                            "color-mix(in srgb, var(--accent) 15%, transparent)",
                          color: "var(--accent)",
                        }}
                      >
                        <span>{TYPE_ICONS[ch.type]}</span>
                        {CHANNEL_TYPES.find((t) => t.value === ch.type)
                          ?.label ??
                          ch.type}
                      </span>
                    </td>
                    <td class="px-4 py-3 font-medium">{ch.name}</td>
                    <td class="px-4 py-3">
                      {ch.lastError
                        ? (
                          <span class="inline-flex items-center gap-1.5">
                            <span
                              class="inline-block h-2 w-2 rounded-full"
                              style={{ backgroundColor: "var(--danger)" }}
                            />
                            <span
                              class="text-xs max-w-[200px] truncate"
                              style={{ color: "var(--danger)" }}
                              title={ch.lastError}
                            >
                              {ch.lastError}
                            </span>
                          </span>
                        )
                        : (
                          <span class="inline-flex items-center gap-1.5">
                            <span
                              class="inline-block h-2 w-2 rounded-full"
                              style={{ backgroundColor: "var(--success)" }}
                            />
                            <span class="text-xs" style={{ opacity: 0.7 }}>
                              OK
                            </span>
                          </span>
                        )}
                    </td>
                    <td class="px-4 py-3">
                      <span class="text-xs font-mono" style={{ opacity: 0.6 }}>
                        {Object.entries(ch.config ?? {}).map(([k, v]) => (
                          <span key={k} class="mr-2">
                            {k}: {String(v)}
                          </span>
                        ))}
                      </span>
                    </td>
                    <td class="px-4 py-3 text-xs" style={{ opacity: 0.7 }}>
                      {formatDate(ch.createdAt)}
                    </td>
                    <td class="px-4 py-3 text-right">
                      <div class="inline-flex items-center gap-2">
                        <button
                          type="button"
                          class="text-xs font-medium hover:underline"
                          style={{ color: "var(--accent)" }}
                          onClick={() => openEdit(ch)}
                        >
                          Edit
                        </button>
                        <button
                          type="button"
                          class="text-xs font-medium hover:underline"
                          style={{ color: "var(--accent)" }}
                          onClick={() => handleTest(ch)}
                        >
                          Test
                        </button>
                        {renderTestResult(ch)}
                        <button
                          type="button"
                          class="text-xs font-medium hover:underline"
                          style={{ color: "var(--danger)" }}
                          onClick={() => {
                            deleteTarget.value = ch;
                            deleteConfirmText.value = "";
                          }}
                        >
                          Delete
                        </button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}

      {/* Create/Edit Modal */}
      {modalOpen.value && (
        <div
          class="fixed inset-0 z-50 flex items-center justify-center"
          style={{ backgroundColor: "rgba(0,0,0,0.5)" }}
          onClick={(e) => {
            if (e.target === e.currentTarget) {
              modalOpen.value = false;
              resetForm();
            }
          }}
        >
          <div
            class="w-full max-w-[480px] rounded-xl p-6 shadow-xl"
            style={{
              backgroundColor: "var(--bg-elevated)",
              border: "1px solid var(--border-primary)",
            }}
          >
            <h3
              class="text-base font-semibold mb-4"
              style={{ color: "var(--text-primary)" }}
            >
              {editingId.value ? "Edit Channel" : "Create Channel"}
            </h3>

            {/* Name */}
            <label class="block text-xs font-medium text-text-primary mb-1">
              Name
            </label>
            <input
              type="text"
              class={inputClass}
              placeholder="my-slack-alerts"
              value={formName.value}
              onInput={(e) =>
                formName.value = (e.target as HTMLInputElement).value}
            />

            {/* Type radio */}
            <fieldset class="mt-4">
              <legend class="text-xs font-medium text-text-primary mb-2">
                Type
              </legend>
              <div class="flex gap-4">
                {CHANNEL_TYPES.map((t) => (
                  <label
                    key={t.value}
                    class="flex items-center gap-1.5 text-sm cursor-pointer"
                    style={{ color: "var(--text-primary)" }}
                  >
                    <input
                      type="radio"
                      name="channel-type"
                      value={t.value}
                      checked={formType.value === t.value}
                      onChange={() => formType.value = t.value}
                    />
                    <span>{TYPE_ICONS[t.value]}</span>
                    {t.label}
                  </label>
                ))}
              </div>
            </fieldset>

            {/* Type-specific config */}
            {renderConfigForm()}

            {/* Actions */}
            <div class="mt-6 flex justify-end gap-2">
              <button
                type="button"
                class="rounded-md px-3 py-1.5 text-sm font-medium"
                style={{
                  color: "var(--text-primary)",
                  backgroundColor: "var(--surface)",
                  border: "1px solid var(--border-primary)",
                }}
                onClick={() => {
                  modalOpen.value = false;
                  resetForm();
                }}
              >
                Cancel
              </button>
              <button
                type="button"
                class="rounded-md px-3 py-1.5 text-sm font-medium text-white"
                style={{ backgroundColor: "var(--accent)" }}
                disabled={saving.value}
                onClick={handleSave}
              >
                {saving.value ? "Saving\u2026" : "Save"}
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Delete Confirmation Modal */}
      {deleteTarget.value && (
        <div
          class="fixed inset-0 z-50 flex items-center justify-center"
          style={{ backgroundColor: "rgba(0,0,0,0.5)" }}
          onClick={(e) => {
            if (e.target === e.currentTarget) {
              deleteTarget.value = null;
              deleteConfirmText.value = "";
            }
          }}
        >
          <div
            class="w-full max-w-[480px] rounded-xl p-6 shadow-xl"
            style={{
              backgroundColor: "var(--bg-elevated)",
              border: "1px solid var(--border-primary)",
            }}
          >
            <h3
              class="text-base font-semibold mb-2"
              style={{ color: "var(--danger)" }}
            >
              Delete Channel
            </h3>
            <p class="text-sm mb-4" style={{ color: "var(--text-primary)" }}>
              This action cannot be undone. Type{" "}
              <strong>{deleteTarget.value.name}</strong> to confirm.
            </p>
            <input
              type="text"
              class={inputClass}
              placeholder={deleteTarget.value.name}
              value={deleteConfirmText.value}
              onInput={(e) =>
                deleteConfirmText.value = (e.target as HTMLInputElement).value}
            />
            <div class="mt-4 flex justify-end gap-2">
              <button
                type="button"
                class="rounded-md px-3 py-1.5 text-sm font-medium"
                style={{
                  color: "var(--text-primary)",
                  backgroundColor: "var(--surface)",
                  border: "1px solid var(--border-primary)",
                }}
                onClick={() => {
                  deleteTarget.value = null;
                  deleteConfirmText.value = "";
                }}
              >
                Cancel
              </button>
              <button
                type="button"
                class="rounded-md px-3 py-1.5 text-sm font-medium text-white"
                style={{ backgroundColor: "var(--danger)" }}
                disabled={saving.value ||
                  deleteConfirmText.value !== deleteTarget.value.name}
                onClick={handleDelete}
              >
                {saving.value ? "Deleting\u2026" : "Delete"}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
