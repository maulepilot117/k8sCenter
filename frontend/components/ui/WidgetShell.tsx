import type { ComponentChildren, JSX } from "preact";
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
export default function WidgetShell(
  { title, action, children, padding = 20, style }: WidgetShellProps,
) {
  return (
    <GlassCard padding={padding} style={style}>
      {(title || action) && (
        <div
          style={{
            display: "flex",
            alignItems: "center",
            justifyContent: "space-between",
            gap: "12px",
            marginBottom: "14px",
          }}
        >
          {title
            ? (
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
            )
            : <span />}
          {action}
        </div>
      )}
      {children}
    </GlassCard>
  );
}
