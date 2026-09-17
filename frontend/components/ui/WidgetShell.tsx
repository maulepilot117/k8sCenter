import type { ComponentChildren, JSX } from "preact";
import { useContext } from "preact/hooks";
import { CellFillContext } from "@/components/dashboard/cell-fill.ts";
import GlassCard from "@/components/ui/GlassCard.tsx";

interface WidgetShellProps {
  title?: string;
  /** right-aligned header slot: legend, badge, menu, time-range tabs */
  action?: ComponentChildren;
  children: ComponentChildren;
  padding?: number;
  style?: JSX.CSSProperties;
}

/**
 * Standard dashboard widget: a GlassCard with a consistent title row.
 * Every metric/list widget on a dashboard should use this so headers,
 * spacing, and type stay identical across the app.
 */
export default function WidgetShell({
  title,
  action,
  children,
  padding = 20,
  style,
}: WidgetShellProps) {
  // In a dashboard grid cell the card takes the cell's height, keeps its title
  // row fixed, and scrolls the body when the content is taller than the cell.
  const fill = useContext(CellFillContext);
  const body = fill ? (
    <div style={{ flex: "1 1 auto", minHeight: 0, overflowY: "auto" }}>
      {children}
    </div>
  ) : (
    children
  );

  return (
    <GlassCard
      padding={padding}
      style={
        fill
          ? {
              height: "100%",
              boxSizing: "border-box",
              display: "flex",
              flexDirection: "column",
              ...style,
            }
          : style
      }
    >
      {(title || action) && (
        <div
          style={{
            display: "flex",
            alignItems: "center",
            justifyContent: "space-between",
            gap: "12px",
            marginBottom: "14px",
            flexShrink: 0,
          }}
        >
          {title ? (
            <h3
              style={{
                margin: 0,
                fontSize: "14px",
                fontWeight: 650,
                color: "var(--text-primary)",
              }}
            >
              {title}
            </h3>
          ) : (
            <span />
          )}
          {action}
        </div>
      )}
      {body}
    </GlassCard>
  );
}
