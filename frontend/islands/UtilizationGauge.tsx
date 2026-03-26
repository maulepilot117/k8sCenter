import { GaugeRing } from "@/components/ui/GaugeRing.tsx";

interface UtilizationGaugeProps {
  title: string;
  value: number;
  used: string;
  total: string;
  requests?: string;
  limits?: string;
  color: string;
  secondaryColor?: string;
}

export default function UtilizationGauge({
  title,
  value,
  used,
  total,
  requests,
  limits,
  color,
  secondaryColor,
}: UtilizationGaugeProps) {
  const statRows: { label: string; value: string }[] = [
    { label: "Used", value: `${used} / ${total}` },
  ];
  if (requests) statRows.push({ label: "Requests", value: requests });
  if (limits) statRows.push({ label: "Limits", value: limits });

  return (
    <div
      style={{
        background: "var(--bg-surface)",
        border: "1px solid var(--border-primary)",
        borderRadius: "var(--radius)",
        padding: "16px",
      }}
    >
      {/* Title */}
      <div
        style={{
          fontSize: "11px",
          fontWeight: 600,
          textTransform: "uppercase",
          letterSpacing: "0.05em",
          color: "var(--text-muted)",
          marginBottom: "12px",
        }}
      >
        {title}
      </div>

      {/* Gauge + stats row */}
      <div style={{ display: "flex", alignItems: "center", gap: "20px" }}>
        <GaugeRing
          value={value}
          size={100}
          strokeWidth={8}
          color={color}
          secondaryColor={secondaryColor}
        />

        {/* Stats table */}
        <div style={{ display: "flex", flexDirection: "column", gap: "8px", flex: 1 }}>
          {statRows.map((row) => (
            <div
              key={row.label}
              style={{
                display: "flex",
                justifyContent: "space-between",
                alignItems: "center",
              }}
            >
              <span style={{ fontSize: "12px", color: "var(--text-muted)" }}>
                {row.label}
              </span>
              <span
                style={{
                  fontSize: "12px",
                  fontFamily: "var(--font-mono, monospace)",
                  color: "var(--text-primary)",
                  fontWeight: 500,
                }}
              >
                {row.value}
              </span>
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}
