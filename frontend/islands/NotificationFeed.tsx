import { useSignal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { notifApi } from "@/lib/api.ts";
import { useWsRefetch } from "@/lib/useWsRefetch.ts";
import { resourceHref } from "@/lib/k8s-links.ts";
import {
  SeverityDot,
  SourceBadge,
} from "@/components/ui/NotifCenterBadges.tsx";
import { timeAgo } from "@/lib/timeAgo.ts";
import type { AppNotification } from "@/lib/notif-center-types.ts";
import { NOTIF_SEVERITIES, NOTIF_SOURCES } from "@/lib/notif-center-types.ts";

const PAGE_SIZE = 25;

export default function NotificationFeed() {
  const notifications = useSignal<AppNotification[]>([]);
  const total = useSignal(0);
  const loading = useSignal(true);
  const page = useSignal(0);
  const filterSource = useSignal<string>("all");
  const filterSeverity = useSignal<string>("all");
  const filterRead = useSignal<string>("all");

  async function fetchData() {
    try {
      const params: Record<string, string | number> = {
        limit: PAGE_SIZE,
        offset: page.value * PAGE_SIZE,
      };
      if (filterSource.value !== "all") params.source = filterSource.value;
      if (filterSeverity.value !== "all") {
        params.severity = filterSeverity.value;
      }
      if (filterRead.value !== "all") params.read = filterRead.value;

      const res = await notifApi.list(
        params as unknown as Parameters<typeof notifApi.list>[0],
      );
      notifications.value = res.data?.data ?? [];
      total.value = res.data?.metadata?.total ?? 0;
    } catch {
      notifications.value = [];
      total.value = 0;
    } finally {
      loading.value = false;
    }
  }

  useEffect(() => {
    if (!IS_BROWSER) return;
    fetchData();
  }, []);

  // Re-fetch when filters or page change
  useEffect(() => {
    if (!IS_BROWSER) return;
    loading.value = true;
    fetchData();
  }, [
    filterSource.value,
    filterSeverity.value,
    filterRead.value,
    page.value,
  ]);

  // Live WS updates with 2s debounce
  useWsRefetch(fetchData, [
    ["notif-feed", "notifications", ""],
  ], 2000);

  function resetAndFilter() {
    page.value = 0;
  }

  function handleSourceChange(e: Event) {
    filterSource.value = (e.target as HTMLSelectElement).value;
    resetAndFilter();
  }

  function handleSeverityChange(e: Event) {
    filterSeverity.value = (e.target as HTMLSelectElement).value;
    resetAndFilter();
  }

  function handleReadChange(e: Event) {
    filterRead.value = (e.target as HTMLSelectElement).value;
    resetAndFilter();
  }

  async function handleRowClick(n: AppNotification) {
    // Mark as read
    if (!n.read) {
      await notifApi.markRead(n.id);
    }
    // Navigate to resource detail if possible
    if (n.resourceKind && n.resourceName) {
      const href = resourceHref(
        n.resourceKind,
        n.resourceNamespace,
        n.resourceName,
      );
      if (href) {
        globalThis.location.href = href;
        return;
      }
    }
  }

  if (!IS_BROWSER) return null;

  const start = page.value * PAGE_SIZE;
  const end = Math.min(start + PAGE_SIZE, total.value);
  const hasNext = end < total.value;
  const hasPrev = page.value > 0;

  const selectStyle = {
    padding: "6px 10px",
    borderRadius: "6px",
    border: "1px solid var(--border-primary)",
    backgroundColor: "var(--surface)",
    color: "var(--text-primary)",
    fontSize: "13px",
    outline: "none",
  };

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "16px" }}>
      {/* Filter bar */}
      <div
        class="flex flex-wrap items-center gap-3"
        style={{
          padding: "12px 16px",
          backgroundColor: "var(--bg-elevated)",
          borderRadius: "8px",
          border: "1px solid var(--border-primary)",
        }}
      >
        <label
          class="flex items-center gap-1.5 text-xs"
          style={{ color: "var(--text-muted)" }}
        >
          Source
          <select
            value={filterSource.value}
            onChange={handleSourceChange}
            style={selectStyle}
          >
            <option value="all">All</option>
            {NOTIF_SOURCES.map((s) => (
              <option key={s} value={s}>
                {s.charAt(0).toUpperCase() + s.slice(1)}
              </option>
            ))}
          </select>
        </label>

        <label
          class="flex items-center gap-1.5 text-xs"
          style={{ color: "var(--text-muted)" }}
        >
          Severity
          <select
            value={filterSeverity.value}
            onChange={handleSeverityChange}
            style={selectStyle}
          >
            <option value="all">All</option>
            {NOTIF_SEVERITIES.map((s) => (
              <option key={s} value={s}>
                {s.charAt(0).toUpperCase() + s.slice(1)}
              </option>
            ))}
          </select>
        </label>

        <label
          class="flex items-center gap-1.5 text-xs"
          style={{ color: "var(--text-muted)" }}
        >
          Status
          <select
            value={filterRead.value}
            onChange={handleReadChange}
            style={selectStyle}
          >
            <option value="all">All</option>
            <option value="read">Read</option>
            <option value="unread">Unread</option>
          </select>
        </label>
      </div>

      {/* Content */}
      {loading.value
        ? (
          <div
            class="flex items-center justify-center"
            style={{ padding: "48px 0", color: "var(--text-muted)" }}
          >
            Loading...
          </div>
        )
        : notifications.value.length === 0
        ? (
          <div
            class="flex items-center justify-center"
            style={{ padding: "48px 0", color: "var(--text-muted)" }}
          >
            No notifications yet
          </div>
        )
        : (
          <>
            {/* Table */}
            <div
              style={{
                borderRadius: "8px",
                border: "1px solid var(--border-primary)",
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
                      backgroundColor: "var(--bg-elevated)",
                      borderBottom: "1px solid var(--border-primary)",
                    }}
                  >
                    <th
                      style={{
                        padding: "10px 12px",
                        textAlign: "left",
                        color: "var(--text-muted)",
                        fontWeight: 500,
                        width: "32px",
                      }}
                    >
                    </th>
                    <th
                      style={{
                        padding: "10px 12px",
                        textAlign: "left",
                        color: "var(--text-muted)",
                        fontWeight: 500,
                        width: "80px",
                      }}
                    >
                      Source
                    </th>
                    <th
                      style={{
                        padding: "10px 12px",
                        textAlign: "left",
                        color: "var(--text-muted)",
                        fontWeight: 500,
                      }}
                    >
                      Title
                    </th>
                    <th
                      style={{
                        padding: "10px 12px",
                        textAlign: "right",
                        color: "var(--text-muted)",
                        fontWeight: 500,
                        width: "120px",
                      }}
                    >
                      Time
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {notifications.value.map((n) => {
                    const isUnread = !n.read;
                    const href = n.resourceKind && n.resourceName
                      ? resourceHref(
                        n.resourceKind,
                        n.resourceNamespace,
                        n.resourceName,
                      )
                      : null;
                    return (
                      <tr
                        key={n.id}
                        onClick={() => handleRowClick(n)}
                        style={{
                          backgroundColor: isUnread
                            ? "color-mix(in srgb, var(--accent) 6%, transparent)"
                            : "transparent",
                          borderBottom: "1px solid var(--border-primary)",
                          cursor: href ? "pointer" : "default",
                          transition: "background-color 0.15s",
                        }}
                        onMouseEnter={(e) => {
                          (e.currentTarget as HTMLTableRowElement).style
                            .backgroundColor = isUnread
                              ? "color-mix(in srgb, var(--accent) 12%, transparent)"
                              : "color-mix(in srgb, var(--text-primary) 4%, transparent)";
                        }}
                        onMouseLeave={(e) => {
                          (e.currentTarget as HTMLTableRowElement).style
                            .backgroundColor = isUnread
                              ? "color-mix(in srgb, var(--accent) 6%, transparent)"
                              : "transparent";
                        }}
                      >
                        <td
                          style={{ padding: "10px 12px", textAlign: "center" }}
                        >
                          <SeverityDot severity={n.severity} />
                        </td>
                        <td style={{ padding: "10px 12px" }}>
                          <SourceBadge source={n.source} />
                        </td>
                        <td
                          style={{
                            padding: "10px 12px",
                            color: isUnread
                              ? "var(--text-primary)"
                              : "var(--text-muted)",
                            fontWeight: isUnread ? 500 : 400,
                          }}
                        >
                          {href
                            ? (
                              <span
                                style={{
                                  color: "var(--accent)",
                                  textDecoration: "none",
                                }}
                              >
                                {n.title}
                              </span>
                            )
                            : n.title}
                        </td>
                        <td
                          style={{
                            padding: "10px 12px",
                            textAlign: "right",
                            color: "var(--text-muted)",
                            whiteSpace: "nowrap",
                          }}
                        >
                          {n.createdAt ? timeAgo(n.createdAt) : "-"}
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>

            {/* Pagination */}
            <div
              class="flex items-center justify-between"
              style={{
                padding: "0 4px",
                color: "var(--text-muted)",
                fontSize: "13px",
              }}
            >
              <span>
                Showing {start + 1}-{end} of {total.value}
              </span>
              <div class="flex items-center gap-2">
                <button
                  type="button"
                  disabled={!hasPrev}
                  onClick={() => {
                    page.value = page.value - 1;
                  }}
                  style={{
                    padding: "6px 14px",
                    borderRadius: "6px",
                    border: "1px solid var(--border-primary)",
                    backgroundColor: "var(--surface)",
                    color: hasPrev
                      ? "var(--text-primary)"
                      : "var(--text-muted)",
                    cursor: hasPrev ? "pointer" : "not-allowed",
                    opacity: hasPrev ? 1 : 0.5,
                    fontSize: "13px",
                  }}
                >
                  Previous
                </button>
                <button
                  type="button"
                  disabled={!hasNext}
                  onClick={() => {
                    page.value = page.value + 1;
                  }}
                  style={{
                    padding: "6px 14px",
                    borderRadius: "6px",
                    border: "1px solid var(--border-primary)",
                    backgroundColor: "var(--surface)",
                    color: hasNext
                      ? "var(--text-primary)"
                      : "var(--text-muted)",
                    cursor: hasNext ? "pointer" : "not-allowed",
                    opacity: hasNext ? 1 : 0.5,
                    fontSize: "13px",
                  }}
                >
                  Next
                </button>
              </div>
            </div>
          </>
        )}
    </div>
  );
}
