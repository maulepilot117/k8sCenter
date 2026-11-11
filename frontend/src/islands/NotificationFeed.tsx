import {
  type ChangeEvent,
  useCallback,
  useEffect,
  useRef,
  useState,
} from "react";
import {
  SeverityDot,
  SourceBadge,
} from "@/components/ui/NotifCenterBadges.tsx";
import { notifApi } from "@/lib/api.ts";
import { useAuth } from "@/lib/auth.ts";
import { notifActionUrl } from "@/lib/notif-action.ts";
import type { AppNotification } from "@/lib/notif-center-types.ts";
import {
  NOTIF_SEVERITIES,
  NOTIF_SOURCES,
  SOURCE_LABELS,
} from "@/lib/notif-center-types.ts";
import { timeAgo } from "@/lib/timeAgo.ts";
import { useWsRefetch } from "@/lib/useWsRefetch.ts";
import { IS_BROWSER } from "@/src/lib/is-browser.ts";
import { useIsClient } from "@/src/lib/use-is-client.ts";
import { useLatest } from "@/src/lib/use-latest.ts";
import { useSignalValue } from "@/src/lib/use-signal-value.ts";

const PAGE_SIZE = 25;

interface FeedQuery {
  page: number;
  source: string;
  severity: string;
  read: string;
}

export default function NotificationFeed() {
  const isClient = useIsClient();
  const [notifications, setNotifications] = useState<AppNotification[]>([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(true);
  const [page, setPage] = useState(0);
  const [filterSource, setFilterSource] = useState<string>("all");
  const [filterSeverity, setFilterSeverity] = useState<string>("all");
  const [filterRead, setFilterRead] = useState<string>("all");
  const fetchSeq = useRef(0); // sequence counter to discard stale fetch results
  const mounted = useRef(true);

  // The live-update refetch runs long after the render that registered it,
  // so it reads the filters and page as they are when it fires.
  const queryRef = useLatest<FeedQuery>({
    page,
    source: filterSource,
    severity: filterSeverity,
    read: filterRead,
  });

  useEffect(
    () => () => {
      mounted.current = false;
    },
    [],
  );

  const fetchData = useCallback(async (query: FeedQuery) => {
    const seq = ++fetchSeq.current;
    try {
      const params: Record<string, string | number> = {
        limit: PAGE_SIZE,
        offset: query.page * PAGE_SIZE,
      };
      if (query.source !== "all") params.source = query.source;
      if (query.severity !== "all") {
        params.severity = query.severity;
      }
      if (query.read !== "all") params.read = query.read;

      const res = await notifApi.list(
        params as unknown as Parameters<typeof notifApi.list>[0],
      );
      if (seq !== fetchSeq.current || !mounted.current) return; // stale response
      setNotifications(res.data ?? []);
      setTotal(res.metadata?.total ?? 0);
    } catch {
      if (seq !== fetchSeq.current || !mounted.current) return;
      setNotifications([]);
      setTotal(0);
    } finally {
      if (seq === fetchSeq.current) setLoading(false);
    }
  }, []);

  // Fetch on mount + re-fetch when filters or page change
  useEffect(() => {
    if (!IS_BROWSER) return;
    setLoading(true);
    fetchData({
      page,
      source: filterSource,
      severity: filterSeverity,
      read: filterRead,
    });
  }, [filterSource, filterSeverity, filterRead, page, fetchData]);

  // Live WS updates with 2s debounce
  useWsRefetch(
    () => fetchData(queryRef.current),
    [["notif-feed", "notifications", ""]],
    2000,
  );

  function resetAndFilter() {
    setPage(0);
  }

  function handleSourceChange(e: ChangeEvent<HTMLSelectElement>) {
    setFilterSource(e.currentTarget.value);
    resetAndFilter();
  }

  function handleSeverityChange(e: ChangeEvent<HTMLSelectElement>) {
    setFilterSeverity(e.currentTarget.value);
    resetAndFilter();
  }

  function handleReadChange(e: ChangeEvent<HTMLSelectElement>) {
    setFilterRead(e.currentTarget.value);
    resetAndFilter();
  }

  const user = useSignalValue(useAuth().user);
  const isAdmin = !!user?.roles?.includes("admin");

  function handleRowClick(n: AppNotification) {
    // Fire-and-forget markRead with keepalive so it survives page navigation
    if (!n.read) {
      notifApi.markReadQuiet(n.id).catch(() => {});
      // Optimistic read-state update
      setNotifications((prev) =>
        prev.map((item) => (item.id === n.id ? { ...item, read: true } : item)),
      );
    }
    const href = notifActionUrl(n, { isAdmin });
    if (href) {
      globalThis.location.href = href;
    }
  }

  if (!isClient) return null;

  const start = page * PAGE_SIZE;
  const end = Math.min(start + PAGE_SIZE, total);
  const hasNext = end < total;
  const hasPrev = page > 0;

  const selectStyle = {
    padding: "6px 10px",
    borderRadius: "6px",
    border: "1px solid var(--border-primary)",
    backgroundColor: "var(--bg-surface)",
    color: "var(--text-primary)",
    fontSize: "13px",
    outline: "none",
  };

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "16px" }}>
      {/* Filter bar */}
      <div
        className="flex flex-wrap items-center gap-3"
        style={{
          padding: "12px 16px",
          backgroundColor: "var(--bg-elevated)",
          borderRadius: "8px",
          border: "1px solid var(--border-primary)",
        }}
      >
        <label
          className="flex items-center gap-1.5 text-xs"
          style={{ color: "var(--text-muted)" }}
        >
          Source
          <select
            value={filterSource}
            onChange={handleSourceChange}
            style={selectStyle}
          >
            <option value="all">All</option>
            {NOTIF_SOURCES.map((s) => (
              <option key={s} value={s}>
                {SOURCE_LABELS[s]}
              </option>
            ))}
          </select>
        </label>

        <label
          className="flex items-center gap-1.5 text-xs"
          style={{ color: "var(--text-muted)" }}
        >
          Severity
          <select
            value={filterSeverity}
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
          className="flex items-center gap-1.5 text-xs"
          style={{ color: "var(--text-muted)" }}
        >
          Status
          <select
            value={filterRead}
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
      {loading ? (
        <div
          className="flex items-center justify-center"
          style={{ padding: "48px 0", color: "var(--text-muted)" }}
        >
          Loading...
        </div>
      ) : notifications.length === 0 ? (
        <div
          className="flex items-center justify-center"
          style={{ padding: "48px 0", color: "var(--text-muted)" }}
        >
          No notifications yet
        </div>
      ) : (
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
                  ></th>
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
                {notifications.map((n) => {
                  const isUnread = !n.read;
                  const href = notifActionUrl(n, { isAdmin });
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
                        e.currentTarget.style.backgroundColor = isUnread
                          ? "color-mix(in srgb, var(--accent) 12%, transparent)"
                          : "color-mix(in srgb, var(--text-primary) 4%, transparent)";
                      }}
                      onMouseLeave={(e) => {
                        e.currentTarget.style.backgroundColor = isUnread
                          ? "color-mix(in srgb, var(--accent) 6%, transparent)"
                          : "transparent";
                      }}
                    >
                      <td style={{ padding: "10px 12px", textAlign: "center" }}>
                        <SeverityDot severity={n.severity} read={n.read} />
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
                        {href ? (
                          <span
                            style={{
                              color: "var(--accent)",
                              textDecoration: "none",
                            }}
                          >
                            {n.title}
                          </span>
                        ) : (
                          n.title
                        )}
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
            className="flex items-center justify-between"
            style={{
              padding: "0 4px",
              color: "var(--text-muted)",
              fontSize: "13px",
            }}
          >
            <span>
              Showing {start + 1}-{end} of {total}
            </span>
            <div className="flex items-center gap-2">
              <button
                type="button"
                disabled={!hasPrev}
                onClick={() => {
                  setPage(page - 1);
                }}
                style={{
                  padding: "6px 14px",
                  borderRadius: "6px",
                  border: "1px solid var(--border-primary)",
                  backgroundColor: "var(--bg-surface)",
                  color: hasPrev ? "var(--text-primary)" : "var(--text-muted)",
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
                  setPage(page + 1);
                }}
                style={{
                  padding: "6px 14px",
                  borderRadius: "6px",
                  border: "1px solid var(--border-primary)",
                  backgroundColor: "var(--bg-surface)",
                  color: hasNext ? "var(--text-primary)" : "var(--text-muted)",
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
