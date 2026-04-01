import { useComputed, useSignal } from "@preact/signals";
import { useEffect, useRef } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { useAuth } from "@/lib/auth.ts";
import { selectedNamespace } from "@/lib/namespace.ts";
import { useNamespaces } from "@/lib/hooks/use-namespaces.ts";
import { initTheme } from "@/lib/themes.ts";
import { initAnimationPrefs } from "@/lib/animation-prefs.ts";
import { selectedCluster } from "@/lib/cluster.ts";
import ThemeSelector from "@/islands/ThemeSelector.tsx";

export default function TopBarV2() {
  const { user, logout, fetchCurrentUser, refreshPermissions } = useAuth();
  const namespaces = useNamespaces();
  const showUserMenu = useSignal(false);
  const menuRef = useRef<HTMLDivElement>(null);

  // Initialize theme and animation prefs on mount
  useEffect(() => {
    if (!IS_BROWSER) return;
    initTheme();
    initAnimationPrefs();
  }, []);

  // Load user info on mount. Always attempt — fetchCurrentUser handles
  // the token refresh flow internally (httpOnly cookie → new access token).
  useEffect(() => {
    if (!IS_BROWSER) return;
    fetchCurrentUser();
  }, []);

  // Close user menu on outside click
  useEffect(() => {
    if (!IS_BROWSER) return;
    const handleClickOutside = (e: MouseEvent) => {
      if (
        showUserMenu.value && menuRef.current &&
        !menuRef.current.contains(e.target as Node)
      ) {
        showUserMenu.value = false;
      }
    };
    document.addEventListener("mousedown", handleClickOutside);
    return () => document.removeEventListener("mousedown", handleClickOutside);
  }, []);

  const displayName = useComputed(() => user.value?.username ?? "User");
  const userRole = useComputed(() => user.value?.roles?.[0] ?? "user");

  return (
    <header
      style={{
        height: "var(--topbar-height, 52px)",
        background: "var(--bg-base)",
        borderBottom: "1px solid var(--border-subtle)",
        display: "flex",
        alignItems: "center",
        justifyContent: "space-between",
        padding: "0 16px",
        gap: "12px",
      }}
    >
      {/* Left section: Cluster + Namespace + Search */}
      <div style={{ display: "flex", alignItems: "center", gap: "12px" }}>
        {/* Cluster indicator */}
        <div
          style={{
            display: "flex",
            alignItems: "center",
            gap: "6px",
            padding: "4px 10px",
            borderRadius: "6px",
            background: "var(--bg-elevated)",
            border: "1px solid var(--border-subtle)",
            fontSize: "13px",
            color: "var(--text-primary)",
          }}
        >
          <span
            style={{
              width: "8px",
              height: "8px",
              borderRadius: "50%",
              background: "var(--success)",
              flexShrink: 0,
            }}
          />
          <span style={{ fontWeight: 500 }}>{selectedCluster.value}</span>
        </div>

        {/* Namespace selector */}
        <select
          aria-label="Select namespace"
          value={selectedNamespace.value}
          onChange={(e) => {
            const ns = (e.target as HTMLSelectElement).value;
            selectedNamespace.value = ns;
            if (ns && ns !== "all") {
              refreshPermissions(ns);
            }
          }}
          style={{
            background: "var(--bg-elevated)",
            color: "var(--text-primary)",
            border: "1px solid var(--border-subtle)",
            borderRadius: "6px",
            padding: "4px 8px",
            fontSize: "13px",
            outline: "none",
            cursor: "pointer",
          }}
        >
          <option value="all">All Namespaces</option>
          {namespaces.value.map((ns) => (
            <option key={ns} value={ns}>{ns}</option>
          ))}
        </select>

        {/* Search trigger */}
        <button
          type="button"
          onClick={() => {
            globalThis.dispatchEvent(new CustomEvent("open-command-palette"));
          }}
          style={{
            display: "flex",
            alignItems: "center",
            gap: "8px",
            padding: "4px 12px",
            borderRadius: "6px",
            background: "var(--bg-elevated)",
            border: "1px solid var(--border-subtle)",
            color: "var(--text-muted)",
            fontSize: "13px",
            cursor: "pointer",
            minWidth: "180px",
          }}
        >
          <svg
            width="14"
            height="14"
            viewBox="0 0 20 20"
            fill="none"
            stroke="currentColor"
            stroke-width="2"
            stroke-linecap="round"
          >
            <circle cx="9" cy="9" r="6" />
            <path d="M13.5 13.5L17 17" />
          </svg>
          <span>Search...</span>
          <span
            style={{
              marginLeft: "auto",
              fontSize: "11px",
              opacity: 0.5,
              border: "1px solid var(--border-subtle)",
              borderRadius: "3px",
              padding: "1px 4px",
            }}
          >
            /
          </span>
        </button>
      </div>

      {/* Right section: theme + notifications + user */}
      <div style={{ display: "flex", alignItems: "center", gap: "8px" }}>
        {/* Theme selector */}
        <ThemeSelector />

        {/* Notification bell */}
        <button
          type="button"
          aria-label="Notifications"
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
          }}
        >
          <svg
            width="18"
            height="18"
            viewBox="0 0 20 20"
            fill="none"
            stroke="currentColor"
            stroke-width="1.5"
            stroke-linecap="round"
            stroke-linejoin="round"
          >
            <path d="M10 2a5 5 0 00-5 5v3l-1.5 2.5h13L15 10V7a5 5 0 00-5-5Z" />
            <path d="M8 16a2 2 0 004 0" />
          </svg>
        </button>

        {/* Divider */}
        <div
          style={{
            width: "1px",
            height: "20px",
            background: "var(--border-subtle)",
          }}
        />

        {/* User avatar + dropdown */}
        <div style={{ position: "relative" }} ref={menuRef}>
          <button
            type="button"
            aria-expanded={showUserMenu.value}
            aria-haspopup="true"
            onClick={() => {
              showUserMenu.value = !showUserMenu.value;
            }}
            style={{
              display: "flex",
              alignItems: "center",
              gap: "8px",
              padding: "4px 8px",
              borderRadius: "6px",
              background: "transparent",
              border: "none",
              color: "var(--text-primary)",
              cursor: "pointer",
              fontSize: "13px",
            }}
          >
            <span
              style={{
                width: "28px",
                height: "28px",
                borderRadius: "50%",
                background: "var(--accent)",
                color: "#fff",
                display: "flex",
                alignItems: "center",
                justifyContent: "center",
                fontSize: "12px",
                fontWeight: 600,
                flexShrink: 0,
              }}
            >
              {displayName.value.charAt(0).toUpperCase()}
            </span>
            <svg
              width="12"
              height="12"
              viewBox="0 0 16 16"
              fill="currentColor"
              style={{ opacity: 0.5 }}
            >
              <path d="M4 6l4 4 4-4" />
            </svg>
          </button>

          {showUserMenu.value && (
            <div
              style={{
                position: "absolute",
                right: 0,
                top: "calc(100% + 4px)",
                width: "200px",
                background: "var(--bg-elevated)",
                border: "1px solid var(--border-primary)",
                borderRadius: "8px",
                boxShadow: "0 4px 16px rgba(0,0,0,0.3)",
                zIndex: 100,
                overflow: "hidden",
              }}
            >
              {/* User info */}
              <div
                style={{
                  padding: "12px 16px",
                  borderBottom: "1px solid var(--border-subtle)",
                }}
              >
                <div
                  style={{
                    fontSize: "13px",
                    fontWeight: 600,
                    color: "var(--text-primary)",
                  }}
                >
                  {displayName.value}
                </div>
                <div
                  style={{
                    fontSize: "11px",
                    color: "var(--text-muted)",
                    marginTop: "2px",
                  }}
                >
                  {userRole.value}
                </div>
              </div>

              {/* Sign out */}
              <button
                type="button"
                onClick={async () => {
                  await logout();
                  globalThis.location.href = "/login";
                }}
                style={{
                  display: "flex",
                  alignItems: "center",
                  gap: "8px",
                  width: "100%",
                  padding: "10px 16px",
                  background: "transparent",
                  border: "none",
                  color: "var(--text-secondary)",
                  fontSize: "13px",
                  cursor: "pointer",
                  textAlign: "left",
                }}
              >
                <svg
                  width="14"
                  height="14"
                  viewBox="0 0 16 16"
                  fill="none"
                  stroke="currentColor"
                  stroke-width="1.5"
                >
                  <path d="M6 14H3a1 1 0 01-1-1V3a1 1 0 011-1h3M11 11l3-3-3-3M14 8H6" />
                </svg>
                Sign out
              </button>
            </div>
          )}
        </div>
      </div>
    </header>
  );
}
