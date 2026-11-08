/** @jsxImportSource react */
import type { CSSProperties, ReactNode } from "react";

interface GlassCardProps {
  children: ReactNode;
  /** "surface" = standard widget; "elevated" = modal/overlay (stronger blur + shadow) */
  tier?: "surface" | "elevated";
  padding?: number | string;
  radius?: number;
  style?: CSSProperties;
  className?: string;
}

/**
 * The canonical floating glass surface. Uses the app's .glass / .glass-elevated
 * classes (backdrop-filter, border, specular rim, shadow) from styles.css.
 *
 * Use for CHROME and WIDGETS only. Data-dense surfaces (tables, YAML/log
 * editors, terminals) should stay solid on var(--bg-surface) for GPU cost and
 * WCAG contrast — see ResourceTable.
 */
export default function GlassCard({
  children,
  tier = "surface",
  padding = 20,
  radius = 18,
  style,
  className: cls,
}: GlassCardProps) {
  const pad = typeof padding === "number" ? `${padding}px` : padding;
  return (
    <div
      className={`${tier === "elevated" ? "glass-elevated" : "glass"} ${cls ?? ""}`}
      style={{ borderRadius: `${radius}px`, padding: pad, ...style }}
    >
      {children}
    </div>
  );
}
