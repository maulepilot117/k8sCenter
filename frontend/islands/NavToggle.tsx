import { navCollapsed, toggleNav } from "@/lib/nav.ts";

// Drop this into TopBarV2's LEFT cluster, before the breadcrumb.
// Shows the secondary nav when it's been collapsed; turns accent when active.
export default function NavToggle() {
  const collapsed = navCollapsed.value;
  return (
    <button
      type="button"
      onClick={toggleNav}
      title="Toggle navigation"
      aria-label="Toggle navigation panel"
      style={{
        width: "36px",
        height: "36px",
        borderRadius: "9px",
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        border: "1px solid var(--border-subtle)",
        background: "var(--bg-elevated)",
        color: collapsed ? "var(--accent)" : "var(--text-secondary)",
        cursor: "pointer",
        flexShrink: 0,
      }}
    >
      <svg
        width="16"
        height="16"
        viewBox="0 0 20 20"
        fill="none"
        stroke="currentColor"
        stroke-width="1.6"
        stroke-linecap="round"
        stroke-linejoin="round"
      >
        <rect x="3" y="4" width="14" height="12" rx="2" />
        <path d="M8 4v12" />
      </svg>
    </button>
  );
}
