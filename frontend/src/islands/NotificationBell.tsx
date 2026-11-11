import { useCallback, useEffect, useRef, useState } from "react";
import {
  SeverityDot,
  SourceBadge,
} from "@/components/ui/NotifCenterBadges.tsx";
import { notifApi } from "@/lib/api.ts";
import { useAuth } from "@/lib/auth.ts";
import { notifActionUrl } from "@/lib/notif-action.ts";
import type { AppNotification } from "@/lib/notif-center-types.ts";
import { timeAgo } from "@/lib/timeAgo.ts";
import { useSignalValue } from "@/src/lib/use-signal-value.ts";
import { subscribe, wsStatus } from "@/src/lib/ws.ts";

export default function NotificationBell() {
  const [unreadCount, setUnreadCount] = useState(0);
  const [showPanel, setShowPanel] = useState(false);
  const [recent, setRecent] = useState<AppNotification[]>([]);
  const [loading, setLoading] = useState(false);
  // Timestamp: the socket callback below is registered once, so it reads the
  // deadline through this ref rather than a value captured at registration.
  const suppressWsUntil = useRef(0);
  const loadingRef = useRef(false);
  const panelRef = useRef<HTMLDivElement>(null);
  const user = useSignalValue(useAuth().user);
  const status = useSignalValue(wsStatus);

  /** Fetch the absolute unread count from the server. */
  const fetchUnreadCount = useCallback(() => {
    notifApi
      .unreadCount()
      .then((res) => {
        setUnreadCount(res.data?.count ?? 0);
      })
      .catch(() => {});
  }, []);

  // Fetch initial unread count on mount
  useEffect(() => {
    fetchUnreadCount();
  }, [fetchUnreadCount]);

  // Subscribe to WS notifications — fetch absolute count instead of blind increment.
  // This resolves badge drift, reconnect staleness, and markAllRead zombies.
  useEffect(() => {
    const unsub = subscribe(
      "notif-bell",
      "notifications",
      "",
      (_eventType, _obj) => {
        // Suppress WS-triggered re-fetches briefly after markAllRead
        if (Date.now() < suppressWsUntil.current) return;
        fetchUnreadCount();
      },
    );
    return unsub;
  }, [fetchUnreadCount]);

  // Re-fetch unread count when WS reconnects (covers missed events during disconnect)
  useEffect(() => {
    if (status === "connected") {
      fetchUnreadCount();
    }
  }, [status, fetchUnreadCount]);

  // Close panel on outside click or Escape — only when panel is open
  useEffect(() => {
    if (!showPanel) return;
    const handleClickOutside = (e: MouseEvent) => {
      if (panelRef.current && !panelRef.current.contains(e.target as Node)) {
        setShowPanel(false);
      }
    };
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") setShowPanel(false);
    };
    document.addEventListener("mousedown", handleClickOutside);
    document.addEventListener("keydown", handleKeyDown);
    return () => {
      document.removeEventListener("mousedown", handleClickOutside);
      document.removeEventListener("keydown", handleKeyDown);
    };
  }, [showPanel]);

  // Fetch recent notifications when panel opens (guarded against double-open)
  const openPanel = async () => {
    if (loadingRef.current) return;
    const opening = !showPanel;
    setShowPanel(opening);
    if (opening) {
      loadingRef.current = true;
      setLoading(true);
      try {
        const res = await notifApi.list({ limit: 20 });
        setRecent(res.data ?? []);
      } catch {
        // Silently fail — panel shows empty state
      }
      loadingRef.current = false;
      setLoading(false);
    }
  };

  const handleMarkAllRead = async () => {
    try {
      // Suppress WS increments for 3s to prevent badge resurrection
      suppressWsUntil.current = Date.now() + 3000;
      await notifApi.markAllRead();
      setUnreadCount(0);
      setRecent((prev) => prev.map((n) => ({ ...n, read: true })));
    } catch {
      suppressWsUntil.current = 0;
    }
  };

  const isAdmin = !!user?.roles?.includes("admin");

  const handleClickNotification = (n: AppNotification) => {
    // Fire-and-forget markRead with keepalive so it survives page navigation
    if (!n.read) {
      notifApi.markReadQuiet(n.id).catch(() => {});
      setUnreadCount((prev) => Math.max(0, prev - 1));
      setRecent((prev) =>
        prev.map((item) => (item.id === n.id ? { ...item, read: true } : item)),
      );
    }
    setShowPanel(false);
    const href = notifActionUrl(n, { isAdmin });
    if (href) {
      globalThis.location.href = href;
    }
  };
  const viewAllHref = isAdmin ? "/admin/notifications" : "/notifications";
  const badgeCount = unreadCount > 99 ? "99+" : unreadCount;

  return (
    <div style={{ position: "relative" }} ref={panelRef}>
      {/* Bell button */}
      <button
        type="button"
        aria-label="Notifications"
        onClick={openPanel}
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          width: "32px",
          height: "32px",
          borderRadius: "6px",
          background: "transparent",
          border: "none",
          color: "var(--text-muted)",
          cursor: "pointer",
          position: "relative",
        }}
      >
        <svg
          width="18"
          height="18"
          viewBox="0 0 20 20"
          fill="none"
          stroke="currentColor"
          strokeWidth="1.5"
          strokeLinecap="round"
          strokeLinejoin="round"
        >
          <path d="M10 2a5 5 0 00-5 5v3l-1.5 2.5h13L15 10V7a5 5 0 00-5-5Z" />
          <path d="M8 16a2 2 0 004 0" />
        </svg>

        {/* Badge */}
        {unreadCount > 0 && (
          <span
            style={{
              position: "absolute",
              top: "2px",
              right: "2px",
              minWidth: "16px",
              height: "16px",
              borderRadius: "8px",
              background: "var(--error)",
              color: "var(--bg-base)",
              fontSize: "10px",
              fontWeight: 600,
              display: "flex",
              alignItems: "center",
              justifyContent: "center",
              padding: "0 4px",
              lineHeight: 1,
            }}
          >
            {badgeCount}
          </span>
        )}
      </button>

      {/* Dropdown panel */}
      {showPanel && (
        <div
          className="glass-elevated"
          style={{
            position: "absolute",
            right: 0,
            top: "calc(100% + 4px)",
            width: "360px",
            maxHeight: "480px",
            overflowY: "auto",
            borderRadius: "8px",
            zIndex: 100,
          }}
        >
          {/* Header */}
          <div
            style={{
              display: "flex",
              alignItems: "center",
              justifyContent: "space-between",
              padding: "12px 16px",
              borderBottom: "1px solid var(--border-subtle)",
            }}
          >
            <span
              style={{
                fontWeight: 600,
                fontSize: "14px",
                color: "var(--text-primary)",
              }}
            >
              Notifications
            </span>
            <button
              type="button"
              onClick={handleMarkAllRead}
              style={{
                background: "none",
                border: "none",
                color: "var(--accent)",
                fontSize: "12px",
                cursor: "pointer",
                padding: 0,
              }}
            >
              Mark all read
            </button>
          </div>

          {/* Notification list */}
          {loading ? (
            <div
              style={{
                padding: "24px",
                textAlign: "center",
                color: "var(--text-muted)",
              }}
            >
              Loading...
            </div>
          ) : recent.length === 0 ? (
            <div
              style={{
                padding: "24px",
                textAlign: "center",
                color: "var(--text-muted)",
                fontSize: "13px",
              }}
            >
              No notifications yet
            </div>
          ) : (
            <div>
              {recent.map((n) => (
                <button
                  key={n.id}
                  type="button"
                  onClick={() => handleClickNotification(n)}
                  style={{
                    display: "flex",
                    alignItems: "flex-start",
                    gap: "8px",
                    width: "100%",
                    padding: "10px 16px",
                    background: n.read
                      ? "transparent"
                      : "color-mix(in srgb, var(--accent) 5%, transparent)",
                    border: "none",
                    borderBottom: "1px solid var(--border-subtle)",
                    cursor: "pointer",
                    textAlign: "left",
                  }}
                >
                  <div style={{ paddingTop: "4px" }}>
                    <SeverityDot severity={n.severity} read={n.read} />
                  </div>
                  <div style={{ flex: 1, minWidth: 0 }}>
                    <div
                      style={{
                        display: "flex",
                        alignItems: "center",
                        gap: "6px",
                        marginBottom: "2px",
                      }}
                    >
                      <SourceBadge source={n.source} />
                      <span
                        style={{
                          fontSize: "11px",
                          color: "var(--text-muted)",
                        }}
                      >
                        {timeAgo(n.createdAt)}
                      </span>
                    </div>
                    <div
                      style={{
                        fontSize: "13px",
                        color: "var(--text-primary)",
                        fontWeight: n.read ? 400 : 500,
                        overflow: "hidden",
                        textOverflow: "ellipsis",
                        whiteSpace: "nowrap",
                      }}
                    >
                      {n.title}
                    </div>
                  </div>
                </button>
              ))}
            </div>
          )}

          {/* Footer */}
          <a
            href={viewAllHref}
            style={{
              display: "block",
              padding: "10px 16px",
              textAlign: "center",
              fontSize: "12px",
              color: "var(--accent)",
              borderTop: "1px solid var(--border-subtle)",
              textDecoration: "none",
            }}
          >
            View all notifications
          </a>
        </div>
      )}
    </div>
  );
}
