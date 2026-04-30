import { useSignal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { notifApi } from "@/lib/api.ts";
import type {
  NotifChannel,
  NotifRule,
  NotifRuleInput,
  NotifSeverity,
  NotifSource,
} from "@/lib/notif-center-types.ts";
import {
  NOTIF_SEVERITIES,
  NOTIF_SOURCE_CATEGORIES,
  SOURCE_LABELS,
} from "@/lib/notif-center-types.ts";

/** Capitalize first letter of a severity for display. */
function capFirst(s: string): string {
  return s.charAt(0).toUpperCase() + s.slice(1);
}

export default function NotificationRules() {
  const rules = useSignal<NotifRule[]>([]);
  const channels = useSignal<NotifChannel[]>([]);
  const loading = useSignal(true);
  const error = useSignal("");

  // Modal state
  const showModal = useSignal(false);
  const editingId = useSignal<string | null>(null);
  const formName = useSignal("");
  const formSources = useSignal<NotifSource[]>([]);
  const formSeverities = useSignal<NotifSeverity[]>([]);
  const formChannelId = useSignal("");
  const formEnabled = useSignal(true);
  const saving = useSignal(false);

  // Delete confirmation
  const confirmDeleteId = useSignal<string | null>(null);

  const fetchRules = async () => {
    try {
      const res = await notifApi.listRules();
      rules.value = res.data ?? [];
    } catch {
      error.value = "Failed to load rules";
    }
    loading.value = false;
  };

  const fetchChannels = async () => {
    try {
      const res = await notifApi.listChannels();
      channels.value = res.data ?? [];
    } catch {
      // channels will be empty — dropdown shows nothing
    }
  };

  useEffect(() => {
    if (!IS_BROWSER) return;
    fetchRules();
    fetchChannels();
  }, []);

  const openCreate = () => {
    editingId.value = null;
    formName.value = "";
    formSources.value = [];
    formSeverities.value = [];
    formChannelId.value = channels.value.length > 0 ? channels.value[0].id : "";
    formEnabled.value = true;
    showModal.value = true;
  };

  const openEdit = (rule: NotifRule) => {
    editingId.value = rule.id;
    formName.value = rule.name;
    formSources.value = [...rule.sourceFilter];
    formSeverities.value = [...rule.severityFilter];
    formChannelId.value = rule.channelId;
    formEnabled.value = rule.enabled;
    showModal.value = true;
  };

  const closeModal = () => {
    showModal.value = false;
    editingId.value = null;
  };

  const handleSave = async () => {
    if (!formName.value.trim() || !formChannelId.value) return;
    saving.value = true;
    const input: NotifRuleInput = {
      name: formName.value.trim(),
      sourceFilter: formSources.value,
      severityFilter: formSeverities.value,
      channelId: formChannelId.value,
      enabled: formEnabled.value,
    };
    try {
      if (editingId.value) {
        await notifApi.updateRule(editingId.value, input);
      } else {
        await notifApi.createRule(input);
      }
      closeModal();
      loading.value = true;
      await fetchRules();
    } catch {
      error.value = "Failed to save rule";
    }
    saving.value = false;
  };

  const togglingIds = useSignal<Set<string>>(new Set());

  const handleToggle = async (rule: NotifRule) => {
    if (togglingIds.value.has(rule.id)) return; // prevent rapid-fire
    togglingIds.value = new Set([...togglingIds.value, rule.id]);
    try {
      await notifApi.updateRule(rule.id, {
        name: rule.name,
        sourceFilter: rule.sourceFilter,
        severityFilter: rule.severityFilter,
        channelId: rule.channelId,
        enabled: !rule.enabled,
      });
      await fetchRules();
    } catch {
      error.value = "Failed to toggle rule";
    }
    const next = new Set(togglingIds.value);
    next.delete(rule.id);
    togglingIds.value = next;
  };

  const handleDelete = async (id: string) => {
    try {
      await notifApi.deleteRule(id);
      confirmDeleteId.value = null;
      loading.value = true;
      await fetchRules();
    } catch {
      error.value = "Failed to delete rule";
    }
  };

  const toggleSource = (src: NotifSource) => {
    const cur = formSources.value;
    formSources.value = cur.includes(src)
      ? cur.filter((s) => s !== src)
      : [...cur, src];
  };

  const toggleSeverity = (sev: NotifSeverity) => {
    const cur = formSeverities.value;
    formSeverities.value = cur.includes(sev)
      ? cur.filter((s) => s !== sev)
      : [...cur, sev];
  };

  // --- Styles ---

  const pillStyle = (color: string): Record<string, string> => ({
    display: "inline-block",
    padding: "2px 8px",
    borderRadius: "9999px",
    fontSize: "11px",
    fontWeight: "500",
    background: `color-mix(in srgb, ${color} 15%, transparent)`,
    color: color,
    marginRight: "4px",
    marginBottom: "2px",
  });

  const btnStyle: Record<string, string> = {
    padding: "6px 14px",
    borderRadius: "6px",
    border: "1px solid var(--border-primary)",
    background: "var(--surface)",
    color: "var(--text-primary)",
    fontSize: "13px",
    cursor: "pointer",
  };

  const primaryBtnStyle: Record<string, string> = {
    ...btnStyle,
    background: "var(--accent)",
    color: "#fff",
    border: "1px solid var(--accent)",
  };

  const dangerBtnStyle: Record<string, string> = {
    ...btnStyle,
    background: "var(--danger)",
    color: "#fff",
    border: "1px solid var(--danger)",
  };

  // --- Render ---

  if (loading.value) {
    return (
      <div
        style={{
          padding: "24px",
          textAlign: "center",
          color: "var(--text-muted)",
        }}
      >
        Loading rules...
      </div>
    );
  }

  return (
    <div>
      {/* Header */}
      <div
        style={{
          display: "flex",
          justifyContent: "space-between",
          alignItems: "center",
          marginBottom: "16px",
        }}
      >
        <h3
          style={{
            margin: 0,
            fontSize: "16px",
            fontWeight: 600,
            color: "var(--text-primary)",
          }}
        >
          Routing Rules
        </h3>
        <button type="button" style={primaryBtnStyle} onClick={openCreate}>
          Create Rule
        </button>
      </div>

      {/* Error banner */}
      {error.value && (
        <div
          style={{
            padding: "8px 12px",
            marginBottom: "12px",
            borderRadius: "6px",
            background: "color-mix(in srgb, var(--danger) 10%, transparent)",
            color: "var(--danger)",
            fontSize: "13px",
          }}
        >
          {error.value}
        </div>
      )}

      {/* Empty state */}
      {rules.value.length === 0
        ? (
          <div
            style={{
              padding: "32px",
              textAlign: "center",
              color: "var(--text-muted)",
              fontSize: "13px",
              border: "1px dashed var(--border-primary)",
              borderRadius: "8px",
            }}
          >
            No rules configured — notifications will only appear in-app
          </div>
        )
        : (
          /* Rules table */
          <div
            style={{
              border: "1px solid var(--border-primary)",
              borderRadius: "8px",
              overflow: "hidden",
            }}
          >
            <table
              style={{
                width: "100%",
                borderCollapse: "collapse",
                fontSize: "13px",
              }}
            >
              <thead>
                <tr
                  style={{
                    background: "var(--surface)",
                    borderBottom: "1px solid var(--border-primary)",
                  }}
                >
                  {["Name", "Sources", "Severities", "Channel", "Enabled", ""]
                    .map(
                      (h) => (
                        <th
                          key={h}
                          style={{
                            padding: "8px 12px",
                            textAlign: "left",
                            fontWeight: 500,
                            color: "var(--text-muted)",
                            fontSize: "12px",
                          }}
                        >
                          {h}
                        </th>
                      ),
                    )}
                </tr>
              </thead>
              <tbody>
                {rules.value.map((rule) => (
                  <tr
                    key={rule.id}
                    style={{
                      borderBottom: "1px solid var(--border-primary)",
                    }}
                  >
                    {/* Name */}
                    <td
                      style={{
                        padding: "10px 12px",
                        color: "var(--text-primary)",
                        fontWeight: 500,
                      }}
                    >
                      {rule.name}
                    </td>

                    {/* Sources */}
                    <td style={{ padding: "10px 12px" }}>
                      {rule.sourceFilter.length === 0
                        ? (
                          <span style={{ color: "var(--text-muted)" }}>
                            All
                          </span>
                        )
                        : rule.sourceFilter.map((src) => (
                          <span key={src} style={pillStyle("var(--accent)")}>
                            {SOURCE_LABELS[src]}
                          </span>
                        ))}
                    </td>

                    {/* Severities */}
                    <td style={{ padding: "10px 12px" }}>
                      {rule.severityFilter.length === 0
                        ? (
                          <span style={{ color: "var(--text-muted)" }}>
                            All
                          </span>
                        )
                        : rule.severityFilter.map((sev) => {
                          const color = sev === "critical"
                            ? "var(--danger)"
                            : sev === "warning"
                            ? "var(--warning)"
                            : "var(--accent)";
                          return (
                            <span key={sev} style={pillStyle(color)}>
                              {capFirst(sev)}
                            </span>
                          );
                        })}
                    </td>

                    {/* Channel */}
                    <td
                      style={{
                        padding: "10px 12px",
                        color: "var(--text-primary)",
                      }}
                    >
                      {rule.channelName ?? rule.channelId}
                    </td>

                    {/* Toggle */}
                    <td style={{ padding: "10px 12px" }}>
                      <button
                        type="button"
                        onClick={() => handleToggle(rule)}
                        style={{
                          padding: "2px 10px",
                          borderRadius: "9999px",
                          border: "none",
                          fontSize: "12px",
                          fontWeight: 500,
                          cursor: "pointer",
                          background: rule.enabled
                            ? "color-mix(in srgb, var(--success) 15%, transparent)"
                            : "color-mix(in srgb, var(--text-muted) 15%, transparent)",
                          color: rule.enabled
                            ? "var(--success)"
                            : "var(--text-muted)",
                        }}
                      >
                        {rule.enabled ? "On" : "Off"}
                      </button>
                    </td>

                    {/* Actions */}
                    <td
                      style={{
                        padding: "10px 12px",
                        whiteSpace: "nowrap",
                      }}
                    >
                      <button
                        type="button"
                        style={{
                          ...btnStyle,
                          padding: "4px 10px",
                          marginRight: "6px",
                        }}
                        onClick={() => openEdit(rule)}
                      >
                        Edit
                      </button>
                      {confirmDeleteId.value === rule.id
                        ? (
                          <>
                            <button
                              type="button"
                              style={{
                                ...dangerBtnStyle,
                                padding: "4px 10px",
                                marginRight: "4px",
                              }}
                              onClick={() => handleDelete(rule.id)}
                            >
                              Confirm
                            </button>
                            <button
                              type="button"
                              style={{ ...btnStyle, padding: "4px 10px" }}
                              onClick={() => confirmDeleteId.value = null}
                            >
                              Cancel
                            </button>
                          </>
                        )
                        : (
                          <button
                            type="button"
                            style={{
                              ...btnStyle,
                              padding: "4px 10px",
                              color: "var(--danger)",
                            }}
                            onClick={() => confirmDeleteId.value = rule.id}
                          >
                            Delete
                          </button>
                        )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}

      {/* Modal overlay */}
      {showModal.value && (
        <div
          style={{
            position: "fixed",
            inset: 0,
            background: "rgba(0,0,0,0.5)",
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
            zIndex: 1000,
          }}
          onClick={(e) => {
            if (e.target === e.currentTarget) closeModal();
          }}
        >
          <div
            style={{
              background: "var(--bg-elevated)",
              border: "1px solid var(--border-primary)",
              borderRadius: "10px",
              padding: "24px",
              width: "100%",
              maxWidth: "480px",
              maxHeight: "80vh",
              overflowY: "auto",
            }}
          >
            <h3
              style={{
                margin: "0 0 16px",
                fontSize: "16px",
                fontWeight: 600,
                color: "var(--text-primary)",
              }}
            >
              {editingId.value ? "Edit Rule" : "Create Rule"}
            </h3>

            {/* Name */}
            <div style={{ marginBottom: "14px" }}>
              <label
                style={{
                  display: "block",
                  fontSize: "12px",
                  fontWeight: 500,
                  color: "var(--text-muted)",
                  marginBottom: "4px",
                }}
              >
                Name
              </label>
              <input
                type="text"
                value={formName.value}
                onInput={(e) =>
                  formName.value = (e.target as HTMLInputElement).value}
                placeholder="e.g. Critical alerts to Slack"
                style={{
                  width: "100%",
                  padding: "8px 10px",
                  borderRadius: "6px",
                  border: "1px solid var(--border-primary)",
                  background: "var(--surface)",
                  color: "var(--text-primary)",
                  fontSize: "13px",
                  boxSizing: "border-box",
                }}
              />
            </div>

            {/* Source filter */}
            <div style={{ marginBottom: "14px" }}>
              <label
                style={{
                  display: "block",
                  fontSize: "12px",
                  fontWeight: 500,
                  color: "var(--text-muted)",
                  marginBottom: "6px",
                }}
              >
                Source filter{" "}
                <span style={{ fontWeight: 400 }}>
                  (empty = all sources)
                </span>
              </label>
              <div
                style={{
                  display: "flex",
                  flexDirection: "column",
                  gap: "10px",
                }}
              >
                {NOTIF_SOURCE_CATEGORIES.map((cat) => (
                  <div key={cat.label}>
                    <div
                      style={{
                        fontSize: "11px",
                        fontWeight: 600,
                        textTransform: "uppercase",
                        letterSpacing: "0.05em",
                        color: "var(--text-muted)",
                        marginBottom: "4px",
                      }}
                    >
                      {cat.label}
                    </div>
                    <div
                      style={{
                        display: "flex",
                        flexWrap: "wrap",
                        gap: "8px 14px",
                      }}
                    >
                      {cat.sources.map((src) => (
                        <label
                          key={src}
                          style={{
                            display: "flex",
                            alignItems: "center",
                            gap: "4px",
                            fontSize: "13px",
                            color: "var(--text-primary)",
                            cursor: "pointer",
                          }}
                        >
                          <input
                            type="checkbox"
                            checked={formSources.value.includes(src)}
                            onChange={() => toggleSource(src)}
                          />
                          {SOURCE_LABELS[src]}
                        </label>
                      ))}
                    </div>
                  </div>
                ))}
              </div>
            </div>

            {/* Severity filter */}
            <div style={{ marginBottom: "14px" }}>
              <label
                style={{
                  display: "block",
                  fontSize: "12px",
                  fontWeight: 500,
                  color: "var(--text-muted)",
                  marginBottom: "6px",
                }}
              >
                Severity filter{" "}
                <span style={{ fontWeight: 400 }}>
                  (empty = all severities)
                </span>
              </label>
              <div style={{ display: "flex", gap: "12px" }}>
                {NOTIF_SEVERITIES.map((sev) => (
                  <label
                    key={sev}
                    style={{
                      display: "flex",
                      alignItems: "center",
                      gap: "4px",
                      fontSize: "13px",
                      color: "var(--text-primary)",
                      cursor: "pointer",
                    }}
                  >
                    <input
                      type="checkbox"
                      checked={formSeverities.value.includes(sev)}
                      onChange={() => toggleSeverity(sev)}
                    />
                    {capFirst(sev)}
                  </label>
                ))}
              </div>
            </div>

            {/* Channel */}
            <div style={{ marginBottom: "14px" }}>
              <label
                style={{
                  display: "block",
                  fontSize: "12px",
                  fontWeight: 500,
                  color: "var(--text-muted)",
                  marginBottom: "4px",
                }}
              >
                Channel
              </label>
              <select
                value={formChannelId.value}
                onChange={(e) =>
                  formChannelId.value = (e.target as HTMLSelectElement).value}
                style={{
                  width: "100%",
                  padding: "8px 10px",
                  borderRadius: "6px",
                  border: "1px solid var(--border-primary)",
                  background: "var(--surface)",
                  color: "var(--text-primary)",
                  fontSize: "13px",
                  boxSizing: "border-box",
                }}
              >
                {channels.value.length === 0 && (
                  <option value="">No channels available</option>
                )}
                {channels.value.map((ch) => (
                  <option key={ch.id} value={ch.id}>
                    {ch.name} ({ch.type})
                  </option>
                ))}
              </select>
            </div>

            {/* Enabled */}
            <div style={{ marginBottom: "20px" }}>
              <label
                style={{
                  display: "flex",
                  alignItems: "center",
                  gap: "6px",
                  fontSize: "13px",
                  color: "var(--text-primary)",
                  cursor: "pointer",
                }}
              >
                <input
                  type="checkbox"
                  checked={formEnabled.value}
                  onChange={() => formEnabled.value = !formEnabled.value}
                />
                Enabled
              </label>
            </div>

            {/* Actions */}
            <div
              style={{
                display: "flex",
                justifyContent: "flex-end",
                gap: "8px",
              }}
            >
              <button type="button" style={btnStyle} onClick={closeModal}>
                Cancel
              </button>
              <button
                type="button"
                style={{
                  ...primaryBtnStyle,
                  opacity: saving.value ? 0.6 : 1,
                }}
                disabled={saving.value || !formName.value.trim() ||
                  !formChannelId.value}
                onClick={handleSave}
              >
                {saving.value
                  ? "Saving..."
                  : editingId.value
                  ? "Update"
                  : "Create"}
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Delete confirmation dialog */}
      {/* (inline in table row — no separate overlay needed) */}
    </div>
  );
}
