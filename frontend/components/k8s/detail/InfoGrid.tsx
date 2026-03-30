import type { ComponentChildren } from "preact";

export interface InfoGridItem {
  label: string;
  value: ComponentChildren;
}

export interface InfoGridProps {
  items: InfoGridItem[];
}

export function InfoGrid({ items }: InfoGridProps) {
  return (
    <div
      style={{
        display: "grid",
        gridTemplateColumns: "repeat(auto-fill, minmax(240px, 1fr))",
        gap: "1px",
        background: "var(--border-subtle)",
        border: "1px solid var(--border-primary)",
        borderRadius: "var(--radius)",
        overflow: "hidden",
        marginBottom: "20px",
      }}
    >
      {items.map((item) => (
        <div
          key={item.label}
          style={{
            background: "var(--bg-surface)",
            padding: "12px 14px",
          }}
        >
          <div
            style={{
              fontSize: "10px",
              fontWeight: 600,
              textTransform: "uppercase",
              letterSpacing: "0.08em",
              color: "var(--text-muted)",
              marginBottom: "4px",
            }}
          >
            {item.label}
          </div>
          <div
            style={{
              fontSize: "13px",
              fontFamily: "var(--font-mono)",
              color: "var(--text-primary)",
            }}
          >
            {item.value}
          </div>
        </div>
      ))}
    </div>
  );
}
