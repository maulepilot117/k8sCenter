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
        padding: "20px",
        transition: "border-color 0.2s ease",
      }}
    >
      {/* Title — matches .card-title from mockup */}
      <div
        style={{
          fontSize: "11px",
          fontWeight: 600,
          textTransform: "uppercase",
          letterSpacing: "0.08em",
          color: "var(--text-muted)",
          marginBottom: "16px",
        }}
      >
        {title}
      </div>

      {/* Gauge + stats row — matches .util-content */}
      <div
        style={{
          display: "flex",
          alignItems: "center",
          gap: "24px",
        }}
      >
        {/* Gauge — matches .gauge-container (100x100, flex-shrink: 0) */}
        <div style={{ flexShrink: 0 }}>
          <GaugeRing
            value={value}
            size={100}
            strokeWidth={10}
            color={color}
            secondaryColor={secondaryColor}
            displayValue={`${Math.round(value)}%`}
            valueSize="22px"
          />
        </div>

        {/* Stats table — matches .util-details */}
        <div style={{ flex: 1 }}>
          {statRows.map((row, idx) => (
            <div
              key={row.label}
              style={{
                display: "flex",
                justifyContent: "space-between",
                alignItems: "center",
                padding: "6px 0",
                fontSize: "13px",
                borderBottom: idx < statRows.length - 1
                  ? "1px solid var(--border-subtle)"
                  : "none",
              }}
            >
              <span style={{ color: "var(--text-secondary)" }}>
                {row.label}
              </span>
              <span
                style={{
                  fontFamily: "var(--font-mono, monospace)",
                  fontWeight: 500,
                  color: "var(--text-primary)",
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
