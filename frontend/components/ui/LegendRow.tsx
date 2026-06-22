export interface LegendRowProps {
  color: string;
  label: string;
  count: number;
}

export function LegendRow({ color, label, count }: LegendRowProps) {
  return (
    <div
      style={{ display: "flex", alignItems: "center", gap: "7px" }}
    >
      <span
        style={{
          width: "8px",
          height: "8px",
          borderRadius: "50%",
          background: color,
          flexShrink: 0,
        }}
      />
      <span
        style={{
          fontSize: "12px",
          color: "var(--text-muted)",
          minWidth: "72px",
        }}
      >
        {label}
      </span>
      <span
        style={{
          fontSize: "12px",
          fontWeight: 600,
          fontVariantNumeric: "tabular-nums",
          color: "var(--text-primary)",
        }}
      >
        {count}
      </span>
    </div>
  );
}
