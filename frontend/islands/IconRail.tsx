// deno-lint-ignore-file react-no-danger
import { useSignal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";

import { DOMAIN_SECTIONS, SETTINGS_SECTION } from "@/lib/constants.ts";
import type { DomainSection } from "@/lib/constants.ts";

/** SVG icon path strings keyed by icon name. viewBox is 0 0 20 20. */
const ICONS: Record<string, string> = {
  grid:
    '<rect x="3" y="3" width="6" height="6" rx="1"/><rect x="11" y="3" width="6" height="6" rx="1"/><rect x="3" y="11" width="6" height="6" rx="1"/><rect x="11" y="11" width="6" height="6" rx="1"/>',
  box:
    '<rect x="3" y="4" width="14" height="12" rx="2"/><path d="M7 4V2M13 4V2"/><circle cx="10" cy="10" r="2"/>',
  globe:
    '<circle cx="10" cy="10" r="7"/><path d="M2 10h4M14 10h4M10 2v4M10 14v4"/>',
  harddrive:
    '<rect x="3" y="5" width="14" height="4" rx="1"/><rect x="3" y="11" width="14" height="4" rx="1"/><circle cx="6" cy="7" r="1" fill="currentColor"/><circle cx="6" cy="13" r="1" fill="currentColor"/>',
  sliders:
    '<circle cx="10" cy="10" r="7"/><circle cx="10" cy="10" r="3"/><path d="M10 3v2M10 15v2M3 10h2M15 10h2"/>',
  shield:
    '<path d="M10 2l1.5 3.5L15 7l-3.5 1.5L10 12 8.5 8.5 5 7l3.5-1.5L10 2Z"/><path d="M4 14l2-1.5M16 14l-2-1.5M10 18v-3"/>',
  activity:
    '<polyline points="3,14 7,8 11,11 14,5 17,9"/><line x1="3" y1="17" x2="17" y2="17"/>',
  wrench: '<path d="M10 4v4l3 2"/><circle cx="10" cy="10" r="7"/>',
  settings:
    '<circle cx="10" cy="10" r="7"/><circle cx="10" cy="10" r="3"/><path d="M13 10h4M3 10h4M10 3v4M10 13v4"/>',
};

function getActiveDomain(path: string): string | null {
  // Special cases
  if (path.startsWith("/cluster")) return "overview";
  if (path.startsWith("/scaling")) return "workloads";
  if (path.startsWith("/admin")) return "settings";
  if (path.startsWith("/settings")) return "settings";

  // Check SETTINGS_SECTION tabs
  if (
    SETTINGS_SECTION.tabs?.some((t) =>
      path === t.href || path.startsWith(t.href + "/")
    )
  ) {
    return "settings";
  }

  // Check each domain section
  for (const section of DOMAIN_SECTIONS) {
    if (section.href === "/" && path === "/") return section.id;
    if (section.href !== "/" && path.startsWith(section.href)) {
      return section.id;
    }
    if (
      section.tabs?.some((t) =>
        path === t.href || path.startsWith(t.href + "/")
      )
    ) {
      return section.id;
    }
  }

  return null;
}

interface IconRailProps {
  currentPath: string;
}

/** Tooltip rendered into document.body via DOM portal to escape stacking contexts */
function RailTooltip(
  { label, top, left }: { label: string; top: number; left: number },
) {
  useEffect(() => {
    const el = document.createElement("div");
    el.className = "rail-tooltip";
    Object.assign(el.style, {
      position: "fixed",
      top: `${top}px`,
      left: `${left}px`,
      transform: "translateY(-50%)",
      background: "var(--bg-elevated)",
      color: "var(--text-primary)",
      padding: "5px 12px",
      borderRadius: "6px",
      fontSize: "12px",
      fontWeight: "500",
      whiteSpace: "nowrap",
      zIndex: "9999",
      border: "1px solid var(--border-subtle)",
      pointerEvents: "none",
      boxShadow:
        "0 4px 12px -2px rgba(0,0,0,0.4), 0 0 0 1px rgba(255,255,255,0.03)",
    });
    el.textContent = label;
    document.body.appendChild(el);
    return () => el.remove();
  }, [label, top, left]);

  return null;
}

export default function IconRail({ currentPath }: IconRailProps) {
  const hoveredId = useSignal<string | null>(null);
  const tooltipPos = useSignal<{ top: number; left: number }>({
    top: 0,
    left: 0,
  });
  const activeDomain = getActiveDomain(currentPath);

  if (!IS_BROWSER) {
    // SSR placeholder with correct dimensions
    return (
      <nav
        style={{
          width: "var(--rail-width, 56px)",
          gridRow: "1 / -1",
          background: "var(--bg-base)",
          borderRight: "1px solid var(--border-subtle)",
        }}
      />
    );
  }

  function RailIcon(
    { section, isActive, isHovered }: {
      section: DomainSection;
      isActive: boolean;
      isHovered: boolean;
    },
  ) {
    const iconPaths = ICONS[section.icon] ?? "";

    return (
      <div
        onMouseEnter={(e: MouseEvent) => {
          const el = e.currentTarget as HTMLElement;
          const rect = el.getBoundingClientRect();
          tooltipPos.value = {
            top: rect.top + rect.height / 2,
            left: rect.right + 10,
          };
          hoveredId.value = section.id;
        }}
        onMouseLeave={() => {
          hoveredId.value = null;
        }}
      >
        <a
          href={section.href}
          aria-label={section.label}
          aria-current={isActive ? "page" : undefined}
          style={{
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
            width: "40px",
            height: "40px",
            borderRadius: "8px",
            color: isActive
              ? "var(--accent)"
              : isHovered
              ? "var(--text-primary)"
              : "var(--text-muted)",
            background: isActive
              ? "var(--accent-dim)"
              : isHovered
              ? "var(--bg-hover)"
              : "transparent",
            transition: "all 150ms ease",
            position: "relative",
            textDecoration: "none",
          }}
        >
          {isActive && (
            <div
              style={{
                position: "absolute",
                left: "-8px",
                top: "8px",
                bottom: "8px",
                width: "3px",
                borderRadius: "0 2px 2px 0",
                background: "var(--accent)",
              }}
            />
          )}
          <svg
            width="20"
            height="20"
            viewBox="0 0 20 20"
            fill="none"
            stroke="currentColor"
            stroke-width="1.5"
            stroke-linecap="round"
            stroke-linejoin="round"
            dangerouslySetInnerHTML={{ __html: iconPaths }}
          />
        </a>
        {isHovered && (
          <RailTooltip
            label={section.label}
            top={tooltipPos.value.top}
            left={tooltipPos.value.left}
          />
        )}
      </div>
    );
  }

  return (
    <nav
      style={{
        width: "var(--rail-width, 56px)",
        gridRow: "1 / -1",
        background: "var(--bg-base)",
        borderRight: "1px solid var(--border-subtle)",
        display: "flex",
        flexDirection: "column",
        alignItems: "center",
        zIndex: 40,
        overflow: "hidden",
      }}
    >
      {/* Logo area — height matches topbar */}
      <div
        style={{
          height: "var(--topbar-height, 48px)",
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          flexShrink: 0,
        }}
      >
        <a href="/" aria-label="k8sCenter home" style={{ display: "flex" }}>
          <svg viewBox="0 0 28 28" fill="none" width="28" height="28">
            <path
              d="M14 2L3 8.5V19.5L14 26L25 19.5V8.5L14 2Z"
              stroke="var(--accent)"
              stroke-width="1.5"
            />
            <circle cx="14" cy="14" r="4" fill="var(--accent)" opacity="0.8" />
            <circle cx="14" cy="6" r="1.5" fill="var(--accent)" />
            <circle cx="7" cy="18" r="1.5" fill="var(--accent)" />
            <circle cx="21" cy="18" r="1.5" fill="var(--accent)" />
          </svg>
        </a>
      </div>

      {/* Domain icons */}
      <div
        style={{
          flex: 1,
          display: "flex",
          flexDirection: "column",
          alignItems: "center",
          gap: "4px",
          paddingTop: "8px",
          overflowY: "auto",
        }}
      >
        {DOMAIN_SECTIONS.map((section) => (
          <RailIcon
            key={section.id}
            section={section}
            isActive={activeDomain === section.id}
            isHovered={hoveredId.value === section.id}
          />
        ))}
      </div>

      {/* Settings at bottom */}
      <div
        style={{
          flexShrink: 0,
          paddingBottom: "12px",
          paddingTop: "8px",
          borderTop: "1px solid var(--border-subtle)",
          width: "100%",
          display: "flex",
          justifyContent: "center",
        }}
      >
        <RailIcon
          section={SETTINGS_SECTION}
          isActive={activeDomain === SETTINGS_SECTION.id}
          isHovered={hoveredId.value === SETTINGS_SECTION.id}
        />
      </div>
    </nav>
  );
}
