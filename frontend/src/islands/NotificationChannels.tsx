/** @jsxImportSource react */
import { useCallback, useEffect, useRef, useState } from "react";
import { notifApi } from "@/lib/api.ts";
import type {
  NotifChannel,
  NotifChannelInput,
  NotifChannelType,
} from "@/lib/notif-center-types.ts";
import { IS_BROWSER } from "@/src/lib/is-browser.ts";
import { showToast } from "@/src/lib/toast-store.ts";

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
  const [channels, setChannels] = useState<NotifChannel[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  // Modal state
  const [modalOpen, setModalOpen] = useState(false);
  const [editingId, setEditingId] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  // Form state
  const [formName, setFormName] = useState("");
  const [formType, setFormType] = useState<NotifChannelType>("slack");
  const [formWebhookUrl, setFormWebhookUrl] = useState("");
  const [formRecipients, setFormRecipients] = useState("");
  const [formSchedule, setFormSchedule] = useState("daily");
  const [formUrl, setFormUrl] = useState("");
  const [formSecret, setFormSecret] = useState("");

  // Delete confirmation
  const [deleteTarget, setDeleteTarget] = useState<NotifChannel | null>(null);
  const [deleteConfirmText, setDeleteConfirmText] = useState("");

  // Test result: channelId -> { ok, timestamp }
  const [testResults, setTestResults] = useState<
    Record<string, { ok: boolean; ts: number }>
  >({});

  const fetchChannels = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const res = await notifApi.listChannels();
      setChannels(res.data ?? []);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to load channels");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    if (!IS_BROWSER) return;
    fetchChannels();
  }, [fetchChannels]);

  function resetForm() {
    setFormName("");
    setFormType("slack");
    setFormWebhookUrl("");
    setFormRecipients("");
    setFormSchedule("daily");
    setFormUrl("");
    setFormSecret("");
    setEditingId(null);
  }

  function openCreate() {
    resetForm();
    setModalOpen(true);
  }

  function openEdit(ch: NotifChannel) {
    setEditingId(ch.id);
    setFormName(ch.name);
    setFormType(ch.type);
    // Pre-fill config (values come masked from API)
    const cfg = ch.config ?? {};
    setFormWebhookUrl((cfg.webhookUrl as string) ?? "");
    setFormRecipients(
      Array.isArray(cfg.recipients)
        ? (cfg.recipients as string[]).join(", ")
        : ((cfg.recipients as string) ?? ""),
    );
    setFormSchedule((cfg.schedule as string) ?? "daily");
    setFormUrl((cfg.url as string) ?? "");
    setFormSecret((cfg.secret as string) ?? "");
    setModalOpen(true);
  }

  function buildInput(): NotifChannelInput {
    const base = { name: formName.trim(), type: formType };
    switch (formType) {
      case "slack":
        return { ...base, config: { webhookUrl: formWebhookUrl.trim() } };
      case "email":
        return {
          ...base,
          config: {
            recipients: formRecipients
              .split(",")
              .map((r) => r.trim())
              .filter(Boolean),
            schedule: formSchedule,
          },
        };
      case "webhook":
        return {
          ...base,
          config: {
            url: formUrl.trim(),
            ...(formSecret.trim() ? { secret: formSecret.trim() } : {}),
          },
        };
    }
  }

  function validateUrl(url: string): boolean {
    try {
      const u = new URL(url);
      return u.protocol === "https:" || u.protocol === "http:";
    } catch {
      return false;
    }
  }

  async function handleSave() {
    if (!formName.trim()) {
      showToast("Name is required", "error");
      return;
    }
    // Client-side URL validation for Slack and Webhook channels
    if (formType === "slack") {
      const url = formWebhookUrl;
      if (!url || !validateUrl(url)) {
        showToast("Valid Slack webhook URL is required", "error");
        return;
      }
    }
    if (formType === "webhook") {
      const url = formUrl;
      if (!url || !validateUrl(url)) {
        showToast("Valid webhook URL is required", "error");
        return;
      }
    }
    setSaving(true);
    try {
      const input = buildInput();
      if (editingId) {
        await notifApi.updateChannel(editingId, input);
        showToast("Channel updated", "success");
      } else {
        await notifApi.createChannel(input);
        showToast("Channel created", "success");
      }
      setModalOpen(false);
      resetForm();
      await fetchChannels();
    } catch (err) {
      showToast(err instanceof Error ? err.message : "Save failed", "error");
    } finally {
      setSaving(false);
    }
  }

  const testTimers = useRef<Record<string, number>>({});

  async function handleTest(ch: NotifChannel) {
    // Clear any previous timer for this channel
    if (testTimers.current[ch.id]) {
      clearTimeout(testTimers.current[ch.id]);
    }
    // Updates use the functional form: another channel's test may settle
    // while this one is in flight, and a captured map would drop its result.
    try {
      await notifApi.testChannel(ch.id);
      setTestResults((prev) => ({
        ...prev,
        [ch.id]: { ok: true, ts: Date.now() },
      }));
    } catch {
      setTestResults((prev) => ({
        ...prev,
        [ch.id]: { ok: false, ts: Date.now() },
      }));
    }
    // Clear after 3 seconds
    testTimers.current[ch.id] = setTimeout(() => {
      setTestResults((prev) => {
        const cur = { ...prev };
        delete cur[ch.id];
        return cur;
      });
      delete testTimers.current[ch.id];
    }, 3000) as unknown as number;
  }

  async function handleDelete() {
    const target = deleteTarget;
    if (!target) return;
    setSaving(true);
    try {
      await notifApi.deleteChannel(target.id);
      showToast(`Deleted "${target.name}"`, "success");
      setDeleteTarget(null);
      setDeleteConfirmText("");
      await fetchChannels();
    } catch (err) {
      showToast(err instanceof Error ? err.message : "Delete failed", "error");
    } finally {
      setSaving(false);
    }
  }

  // --- Render helpers ---

  const inputClass =
    "w-full rounded-md border border-border-primary bg-surface px-3 py-2 text-sm text-text-primary";

  function renderConfigForm() {
    switch (formType) {
      case "slack":
        return (
          <div className="mt-3">
            <label className="block text-xs font-medium text-text-primary mb-1">
              Webhook URL
            </label>
            <input
              type="url"
              className={inputClass}
              placeholder="https://hooks.slack.com/services/..."
              value={formWebhookUrl}
              onChange={(e) => setFormWebhookUrl(e.currentTarget.value)}
            />
          </div>
        );
      case "email":
        return (
          <div className="mt-3 space-y-3">
            <div>
              <label className="block text-xs font-medium text-text-primary mb-1">
                Recipients (comma-separated)
              </label>
              <input
                type="text"
                className={inputClass}
                placeholder="admin@example.com, ops@example.com"
                value={formRecipients}
                onChange={(e) => setFormRecipients(e.currentTarget.value)}
              />
            </div>
            <div>
              <label className="block text-xs font-medium text-text-primary mb-1">
                Schedule
              </label>
              <select
                className={inputClass}
                value={formSchedule}
                onChange={(e) => setFormSchedule(e.currentTarget.value)}
              >
                <option value="daily">Daily digest</option>
                <option value="weekly">Weekly digest</option>
              </select>
            </div>
          </div>
        );
      case "webhook":
        return (
          <div className="mt-3 space-y-3">
            <div>
              <label className="block text-xs font-medium text-text-primary mb-1">
                URL
              </label>
              <input
                type="url"
                className={inputClass}
                placeholder="https://example.com/webhook"
                value={formUrl}
                onChange={(e) => setFormUrl(e.currentTarget.value)}
              />
            </div>
            <div>
              <label className="block text-xs font-medium text-text-primary mb-1">
                Secret (optional)
              </label>
              <input
                type="password"
                className={inputClass}
                placeholder="HMAC signing secret"
                value={formSecret}
                onChange={(e) => setFormSecret(e.currentTarget.value)}
              />
            </div>
          </div>
        );
    }
  }

  function renderTestResult(ch: NotifChannel) {
    const result = testResults[ch.id];
    if (!result) return null;
    return result.ok ? (
      <span className="ml-2 text-xs" style={{ color: "var(--success)" }}>
        {"\u2713"}
      </span>
    ) : (
      <span className="ml-2 text-xs" style={{ color: "var(--error)" }}>
        {"\u2717"}
      </span>
    );
  }

  // --- Main render ---

  if (loading && channels.length === 0) {
    return (
      <div className="flex items-center justify-center py-12">
        <div
          className="h-6 w-6 animate-spin rounded-full border-2 border-current border-t-transparent"
          style={{ color: "var(--accent)" }}
        />
      </div>
    );
  }

  if (error) {
    return (
      <div
        className="rounded-md px-4 py-3 text-sm"
        style={{
          backgroundColor: "color-mix(in srgb, var(--error) 10%, transparent)",
          color: "var(--error)",
        }}
      >
        {error}
      </div>
    );
  }

  return (
    <div>
      {/* Header */}
      <div className="flex items-center justify-between mb-4">
        <h2
          className="text-lg font-semibold"
          style={{ color: "var(--text-primary)" }}
        >
          Notification Channels
        </h2>
        <button
          type="button"
          className="rounded-md px-3 py-1.5 text-sm font-medium"
          style={{ backgroundColor: "var(--accent)", color: "var(--bg-base)" }}
          onClick={openCreate}
        >
          Create Channel
        </button>
      </div>

      {/* Table */}
      {channels.length === 0 ? (
        <p
          className="py-8 text-center text-sm"
          style={{ color: "var(--text-primary)", opacity: 0.6 }}
        >
          No channels configured yet.
        </p>
      ) : (
        <div
          className="overflow-x-auto rounded-lg border"
          style={{
            borderColor: "var(--border-primary)",
            backgroundColor: "var(--bg-elevated)",
          }}
        >
          <table
            className="w-full text-sm"
            style={{ color: "var(--text-primary)" }}
          >
            <thead>
              <tr
                className="text-left text-xs uppercase tracking-wider"
                style={{
                  borderBottom: "1px solid var(--border-primary)",
                  color: "var(--text-primary)",
                  opacity: 0.7,
                }}
              >
                <th className="px-4 py-3 font-medium">Type</th>
                <th className="px-4 py-3 font-medium">Name</th>
                <th className="px-4 py-3 font-medium">Status</th>
                <th className="px-4 py-3 font-medium">Config</th>
                <th className="px-4 py-3 font-medium">Created</th>
                <th className="px-4 py-3 font-medium text-right">Actions</th>
              </tr>
            </thead>
            <tbody>
              {channels.map((ch) => (
                <tr
                  key={ch.id}
                  className="border-t"
                  style={{ borderColor: "var(--border-primary)" }}
                >
                  <td className="px-4 py-3">
                    <span
                      className="inline-flex items-center gap-1.5 rounded-full px-2 py-0.5 text-xs font-medium"
                      style={{
                        backgroundColor:
                          "color-mix(in srgb, var(--accent) 15%, transparent)",
                        color: "var(--accent)",
                      }}
                    >
                      <span>{TYPE_ICONS[ch.type]}</span>
                      {CHANNEL_TYPES.find((t) => t.value === ch.type)?.label ??
                        ch.type}
                    </span>
                  </td>
                  <td className="px-4 py-3 font-medium">{ch.name}</td>
                  <td className="px-4 py-3">
                    {ch.lastError ? (
                      <span className="inline-flex items-center gap-1.5">
                        <span
                          className="inline-block h-2 w-2 rounded-full"
                          style={{ backgroundColor: "var(--error)" }}
                        />
                        <span
                          className="text-xs max-w-[200px] truncate"
                          style={{ color: "var(--error)" }}
                          title={ch.lastError}
                        >
                          {ch.lastError}
                        </span>
                      </span>
                    ) : (
                      <span className="inline-flex items-center gap-1.5">
                        <span
                          className="inline-block h-2 w-2 rounded-full"
                          style={{ backgroundColor: "var(--success)" }}
                        />
                        <span className="text-xs" style={{ opacity: 0.7 }}>
                          OK
                        </span>
                      </span>
                    )}
                  </td>
                  <td className="px-4 py-3">
                    <span
                      className="text-xs font-mono"
                      style={{ opacity: 0.6 }}
                    >
                      {Object.entries(ch.config ?? {}).map(([k, v]) => (
                        <span key={k} className="mr-2">
                          {k}: {String(v)}
                        </span>
                      ))}
                    </span>
                  </td>
                  <td className="px-4 py-3 text-xs" style={{ opacity: 0.7 }}>
                    {formatDate(ch.createdAt)}
                  </td>
                  <td className="px-4 py-3 text-right">
                    <div className="inline-flex items-center gap-2">
                      <button
                        type="button"
                        className="text-xs font-medium hover:underline"
                        style={{ color: "var(--accent)" }}
                        onClick={() => openEdit(ch)}
                      >
                        Edit
                      </button>
                      <button
                        type="button"
                        className="text-xs font-medium hover:underline"
                        style={{ color: "var(--accent)" }}
                        onClick={() => handleTest(ch)}
                      >
                        Test
                      </button>
                      {renderTestResult(ch)}
                      <button
                        type="button"
                        className="text-xs font-medium hover:underline"
                        style={{ color: "var(--error)" }}
                        onClick={() => {
                          setDeleteTarget(ch);
                          setDeleteConfirmText("");
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
      {modalOpen && (
        <div
          className="fixed inset-0 z-50 flex items-center justify-center glass-scrim"
          onClick={(e) => {
            if (e.target === e.currentTarget) {
              setModalOpen(false);
              resetForm();
            }
          }}
        >
          <div className="w-full max-w-[480px] glass-elevated rounded-2xl p-6">
            <h3
              className="text-base font-semibold mb-4"
              style={{ color: "var(--text-primary)" }}
            >
              {editingId ? "Edit Channel" : "Create Channel"}
            </h3>

            {/* Name */}
            <label className="block text-xs font-medium text-text-primary mb-1">
              Name
            </label>
            <input
              type="text"
              className={inputClass}
              placeholder="my-slack-alerts"
              value={formName}
              onChange={(e) => setFormName(e.currentTarget.value)}
            />

            {/* Type radio */}
            <fieldset className="mt-4">
              <legend className="text-xs font-medium text-text-primary mb-2">
                Type
              </legend>
              <div className="flex gap-4">
                {CHANNEL_TYPES.map((t) => (
                  <label
                    key={t.value}
                    className="flex items-center gap-1.5 text-sm cursor-pointer"
                    style={{ color: "var(--text-primary)" }}
                  >
                    <input
                      type="radio"
                      name="channel-type"
                      value={t.value}
                      checked={formType === t.value}
                      onChange={() => setFormType(t.value)}
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
            <div className="mt-6 flex justify-end gap-2">
              <button
                type="button"
                className="rounded-md px-3 py-1.5 text-sm font-medium"
                style={{
                  color: "var(--text-primary)",
                  backgroundColor: "var(--bg-surface)",
                  border: "1px solid var(--border-primary)",
                }}
                onClick={() => {
                  setModalOpen(false);
                  resetForm();
                }}
              >
                Cancel
              </button>
              <button
                type="button"
                className="rounded-md px-3 py-1.5 text-sm font-medium"
                style={{
                  backgroundColor: "var(--accent)",
                  color: "var(--bg-base)",
                }}
                disabled={saving}
                onClick={handleSave}
              >
                {saving ? "Saving\u2026" : "Save"}
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Delete Confirmation Modal */}
      {deleteTarget && (
        <div
          className="fixed inset-0 z-50 flex items-center justify-center glass-scrim"
          onClick={(e) => {
            if (e.target === e.currentTarget) {
              setDeleteTarget(null);
              setDeleteConfirmText("");
            }
          }}
        >
          <div className="w-full max-w-[480px] glass-elevated rounded-2xl p-6">
            <h3
              className="text-base font-semibold mb-2"
              style={{ color: "var(--error)" }}
            >
              Delete Channel
            </h3>
            <p
              className="text-sm mb-4"
              style={{ color: "var(--text-primary)" }}
            >
              This action cannot be undone. Type{" "}
              <strong>{deleteTarget.name}</strong> to confirm.
            </p>
            <input
              type="text"
              className={inputClass}
              placeholder={deleteTarget.name}
              value={deleteConfirmText}
              onChange={(e) => setDeleteConfirmText(e.currentTarget.value)}
            />
            <div className="mt-4 flex justify-end gap-2">
              <button
                type="button"
                className="rounded-md px-3 py-1.5 text-sm font-medium"
                style={{
                  color: "var(--text-primary)",
                  backgroundColor: "var(--bg-surface)",
                  border: "1px solid var(--border-primary)",
                }}
                onClick={() => {
                  setDeleteTarget(null);
                  setDeleteConfirmText("");
                }}
              >
                Cancel
              </button>
              <button
                type="button"
                className="rounded-md px-3 py-1.5 text-sm font-medium"
                style={{
                  backgroundColor: "var(--error)",
                  color: "var(--bg-base)",
                }}
                disabled={saving || deleteConfirmText !== deleteTarget.name}
                onClick={handleDelete}
              >
                {saving ? "Deleting\u2026" : "Delete"}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
