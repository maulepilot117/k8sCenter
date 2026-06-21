import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import {
  domainById,
  getActiveDomain,
  type Health,
  type NavItem,
} from "@/lib/constants.ts";
import { navCollapsed, toggleNav } from "@/lib/nav.ts";

function dotColor(h?: Health): string {
  return h === "ok"
    ? "var(--success)"
    : h === "warn"
    ? "var(--warning)"
    : h === "crit"
    ? "var(--error)"
    : "var(--border-primary)";
}

interface SecondaryNavProps {
  currentPath: string;
}

/**
 * Grouped, filterable, collapsible secondary navigation. Renders the active
 * domain's groups vertically — the fix for horizontal tab-strip scrolling.
 *
 * Width is controlled by the parent grid column (var(--panel-width)); this
 * island flips that var to 0px when collapsed via the navCollapsed signal.
 * The snap is instant — NO CSS transition on the grid track.
 */
export default function SecondaryNav({ currentPath }: SecondaryNavProps) {
  const query = useSignal("");
  const domainId = getActiveDomain(currentPath);
  const domain = domainById(domainId);

  // Keep the grid column width in sync with the collapse state.
  if (IS_BROWSER) {
    document.documentElement.style.setProperty(
      "--panel-width",
      navCollapsed.value ? "0px" : "250px",
    );
  }

  // Overview has no children — collapse the panel for it.
  if (!domain?.groups?.length) {
    return (
      <nav
        class="glass-bar"
        style={{
          width: "100%",
          borderRight: "1px solid var(--glass-border)",
        }}
      />
    );
  }

  const q = query.value.trim().toLowerCase();
  const groups = domain.groups
    .map((g) => ({
      header: g.header,
      items: g.items.filter((it) => !q || it.label.toLowerCase().includes(q)),
    }))
    .filter((g) => g.items.length);

  const isActive = (it: NavItem) =>
    currentPath === it.href || currentPath.startsWith(it.href + "/");

  return (
    <nav
      class="glass-bar"
      style={{
        width: "100%",
        minWidth: 0,
        display: "flex",
        flexDirection: "column",
        overflow: "hidden",
        borderRight: "1px solid var(--glass-border)",
        zIndex: 30,
      }}
    >
      {/* header */}
      <div
        style={{
          height: "var(--topbar-height, 56px)",
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          padding: "0 18px",
          flexShrink: 0,
          borderBottom: "1px solid var(--border-subtle)",
        }}
      >
        <span
          style={{
            fontSize: "15px",
            fontWeight: 650,
            letterSpacing: "-0.01em",
            whiteSpace: "nowrap",
            color: "var(--text-primary)",
          }}
        >
          {domain.label}
        </span>
        <button
          type="button"
          onClick={toggleNav}
          title="Collapse panel"
          aria-label="Collapse navigation panel"
          style={{
            width: "36px",
            height: "36px",
            borderRadius: "7px",
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
            border: "none",
            cursor: "pointer",
            background: "transparent",
            color: "var(--text-muted)",
            flexShrink: 0,
          }}
        >
          <svg
            width="16"
            height="16"
            viewBox="0 0 20 20"
            fill="none"
            stroke="currentColor"
            stroke-width="1.7"
            stroke-linecap="round"
            stroke-linejoin="round"
          >
            <path d="M12 5l-5 5 5 5" />
          </svg>
        </button>
      </div>

      {/* filter */}
      <div style={{ padding: "12px 12px 6px", flexShrink: 0 }}>
        <div
          style={{
            display: "flex",
            alignItems: "center",
            gap: "8px",
            padding: "7px 10px",
            borderRadius: "9px",
            background: "var(--bg-elevated)",
            border: "1px solid var(--border-subtle)",
          }}
        >
          <svg
            width="14"
            height="14"
            viewBox="0 0 20 20"
            fill="none"
            stroke="var(--text-muted)"
            stroke-width="2"
            stroke-linecap="round"
          >
            <circle cx="9" cy="9" r="6" />
            <path d="M13.5 13.5 17 17" />
          </svg>
          <input
            value={query.value}
            onInput={(
              e,
            ) => (query.value = (e.target as HTMLInputElement).value)}
            placeholder="Filter in this section"
            style={{
              background: "transparent",
              border: "none",
              outline: "none",
              color: "var(--text-primary)",
              fontSize: "13px",
              width: "100%",
              fontFamily: "inherit",
            }}
          />
        </div>
      </div>

      {/* grouped items */}
      <div style={{ flex: 1, overflowY: "auto", padding: "2px 10px 16px" }}>
        {groups.map((g) => (
          <div key={g.header} style={{ marginTop: "14px" }}>
            <div
              style={{
                fontSize: "11px",
                fontWeight: 600,
                textTransform: "uppercase",
                letterSpacing: "0.07em",
                color: "var(--text-muted)",
                padding: "0 9px 7px",
              }}
            >
              {g.header}
            </div>
            {g.items.map((it) => {
              const active = isActive(it);
              return (
                <a
                  key={it.href}
                  href={it.href}
                  style={{
                    display: "flex",
                    alignItems: "center",
                    gap: "10px",
                    padding: "8px 9px",
                    borderRadius: "8px",
                    textDecoration: "none",
                    marginBottom: "1px",
                    transition: "background 120ms ease",
                    background: active
                      ? "color-mix(in srgb, var(--accent) 16%, transparent)"
                      : "transparent",
                    color: active
                      ? "var(--text-primary)"
                      : "var(--text-secondary)",
                  }}
                  onMouseEnter={(e) => {
                    if (!active) {
                      (e.currentTarget as HTMLElement).style.background =
                        "var(--bg-hover)";
                    }
                  }}
                  onMouseLeave={(e) => {
                    if (!active) {
                      (e.currentTarget as HTMLElement).style.background =
                        "transparent";
                    }
                  }}
                >
                  <span
                    style={{
                      width: "6px",
                      height: "6px",
                      borderRadius: "50%",
                      flexShrink: 0,
                      background: dotColor(it.health),
                    }}
                  />
                  <span
                    style={{
                      fontSize: "13px",
                      fontWeight: 500,
                      flex: 1,
                      whiteSpace: "nowrap",
                      overflow: "hidden",
                      textOverflow: "ellipsis",
                    }}
                  >
                    {it.label}
                  </span>
                  {/* Replace with a live count from your resource store when kind+count set */}
                  {it.count && it.kind && (
                    <span
                      style={{
                        fontSize: "11px",
                        fontWeight: 600,
                        color: "var(--text-muted)",
                        fontVariantNumeric: "tabular-nums",
                      }}
                      data-count-kind={it.kind}
                    />
                  )}
                </a>
              );
            })}
          </div>
        ))}
      </div>
    </nav>
  );
}
