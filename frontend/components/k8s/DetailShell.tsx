import type { ComponentChildren } from "preact";
import GlassCard from "@/components/ui/GlassCard.tsx";
import StatusBadge, { type Tone } from "@/components/ui/glass/StatusBadge.tsx";

export interface DetailTab {
  id: string;
  label: string;
}

interface DetailShellProps {
  icon?: ComponentChildren;
  title: string;
  subtitle?: string;
  status?: { label: string; tone: Tone };
  /** header action buttons (Scale, Restart, Edit, …) */
  actions?: ComponentChildren;
  tabs: DetailTab[];
  active: string;
  onTab: (id: string) => void;
  /** optional right-hand live-metrics rail */
  rail?: ComponentChildren;
  children: ComponentChildren;
}

/**
 * Standard resource detail page: glass header card (icon + title + status +
 * actions + tab strip) and a body that's either full width or a 1fr / 300px
 * split with a live-metrics rail. Use for ALL 37 detail views so the header,
 * tabs, and rail are pixel-identical everywhere.
 */
export default function DetailShell(
  {
    icon,
    title,
    subtitle,
    status,
    actions,
    tabs,
    active,
    onTab,
    rail,
    children,
  }: DetailShellProps,
) {
  return (
    <div style={{ maxWidth: "1480px", margin: "0 auto" }}>
      <GlassCard padding={20} style={{ marginBottom: "var(--grid-gap, 20px)" }}>
        <div
          style={{
            display: "flex",
            alignItems: "flex-start",
            justifyContent: "space-between",
            gap: "16px",
            flexWrap: "wrap",
          }}
        >
          <div style={{ display: "flex", gap: "14px", alignItems: "center" }}>
            {icon && (
              <span
                style={{
                  width: "44px",
                  height: "44px",
                  borderRadius: "12px",
                  display: "flex",
                  alignItems: "center",
                  justifyContent: "center",
                  background: "var(--accent-dim)",
                  color: "var(--accent)",
                  flexShrink: 0,
                }}
              >
                {icon}
              </span>
            )}
            <div>
              <div
                style={{ display: "flex", alignItems: "center", gap: "10px" }}
              >
                <h1
                  style={{
                    margin: 0,
                    fontSize: "21px",
                    fontWeight: 700,
                    letterSpacing: "-0.02em",
                  }}
                >
                  {title}
                </h1>
                {status && (
                  <StatusBadge label={status.label} tone={status.tone} />
                )}
              </div>
              {subtitle && (
                <div
                  style={{
                    fontSize: "12px",
                    color: "var(--text-muted)",
                    marginTop: "3px",
                  }}
                >
                  {subtitle}
                </div>
              )}
            </div>
          </div>
          {actions && (
            <div style={{ display: "flex", gap: "8px" }}>{actions}</div>
          )}
        </div>

        {/* tabs */}
        <div
          role="tablist"
          style={{
            display: "flex",
            gap: "4px",
            marginTop: "18px",
            marginBottom: "-20px",
            borderBottom: "1px solid var(--border-subtle)",
          }}
        >
          {tabs.map((t) => {
            const on = t.id === active;
            return (
              <button
                key={t.id}
                type="button"
                role="tab"
                aria-selected={on}
                onClick={() => onTab(t.id)}
                style={{
                  padding: "9px 14px",
                  fontSize: "13px",
                  fontWeight: 600,
                  fontFamily: "inherit",
                  cursor: "pointer",
                  background: "transparent",
                  border: "none",
                  borderBottom: `2px solid ${
                    on ? "var(--accent)" : "transparent"
                  }`,
                  color: on ? "var(--text-primary)" : "var(--text-muted)",
                }}
              >
                {t.label}
              </button>
            );
          })}
        </div>
      </GlassCard>

      {rail
        ? (
          <div
            style={{
              display: "grid",
              gridTemplateColumns: "minmax(0, 1fr) 300px",
              gap: "var(--grid-gap, 20px)",
              alignItems: "start",
            }}
          >
            <div style={{ minWidth: 0 }}>{children}</div>
            {rail}
          </div>
        )
        : children}
    </div>
  );
}
