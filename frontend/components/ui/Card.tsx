import type { ReactNode } from "react";

interface CardProps {
  title?: string;
  children: ReactNode;
  className?: string;
  /** Liquid glass chrome treatment. Default solid — data-dense content
   * (tables, editors, logs) must stay on an opaque surface. */
  glass?: boolean;
}

export function Card({ title, children, className, glass = false }: CardProps) {
  return (
    <div
      className={`${glass ? "glass rounded-2xl" : "rounded-lg border"} p-6 ${
        className ?? ""
      }`}
      style={
        glass
          ? undefined
          : {
              background: "var(--bg-surface)",
              borderColor: "var(--border-subtle)",
            }
      }
    >
      {title && (
        <h3
          className="mb-4 text-sm font-semibold uppercase tracking-wider"
          style={{ color: "var(--text-muted)" }}
        >
          {title}
        </h3>
      )}
      {children}
    </div>
  );
}
