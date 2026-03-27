import { age } from "@/lib/format.ts";

export interface ConditionsGridProps {
  conditions: {
    type: string;
    status: string;
    reason?: string;
    message?: string;
    lastTransitionTime?: string;
  }[];
}

function SectionTitle({ title }: { title: string }) {
  return (
    <div
      style={{
        display: "flex",
        alignItems: "center",
        gap: "8px",
        marginBottom: "10px",
      }}
    >
      <span
        style={{
          fontSize: "11px",
          fontWeight: 600,
          textTransform: "uppercase",
          letterSpacing: "0.08em",
          color: "var(--text-muted)",
        }}
      >
        {title}
      </span>
      <span
        style={{ flex: 1, height: "1px", background: "var(--border-subtle)" }}
      />
    </div>
  );
}

export { SectionTitle };

export function ConditionsGrid({ conditions }: ConditionsGridProps) {
  return (
    <div style={{ marginBottom: "20px" }}>
      <SectionTitle title="Conditions" />
      <div
        style={{
          border: "1px solid var(--border-primary)",
          borderRadius: "var(--radius)",
          overflow: "hidden",
        }}
      >
        {conditions.map((c, i) => (
          <div
            key={c.type}
            style={{
              display: "grid",
              gridTemplateColumns: "140px 60px 1fr 80px",
              gap: "12px",
              padding: "10px 14px",
              borderBottom: i < conditions.length - 1
                ? "1px solid var(--border-subtle)"
                : "none",
              fontSize: "12px",
              alignItems: "center",
              cursor: "default",
            }}
            onMouseOver={(e) => {
              (e.currentTarget as HTMLElement).style.background =
                "var(--bg-elevated)";
            }}
            onMouseOut={(e) => {
              (e.currentTarget as HTMLElement).style.background = "";
            }}
          >
            <span style={{ fontWeight: 500, color: "var(--text-primary)" }}>
              {c.type}
            </span>
            <span
              style={{
                fontFamily: "var(--font-mono)",
                fontWeight: 500,
                color: c.status === "True" ? "var(--success)" : "var(--error)",
              }}
            >
              {c.status}
            </span>
            <span style={{ color: "var(--text-secondary)" }}>
              {c.message ?? c.reason ?? "-"}
            </span>
            <span
              style={{
                color: "var(--text-muted)",
                fontFamily: "var(--font-mono)",
              }}
            >
              {c.lastTransitionTime ? age(c.lastTransitionTime) : "-"}
            </span>
          </div>
        ))}
      </div>
    </div>
  );
}
