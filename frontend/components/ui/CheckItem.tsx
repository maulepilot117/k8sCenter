export interface CheckItemProps {
  label: string;
  value: string;
  status: "success" | "warning" | "error";
}

export function CheckItem({ label, value, status }: CheckItemProps) {
  const color = status === "success"
    ? "var(--success)"
    : status === "warning"
    ? "var(--warning)"
    : "var(--error)";
  return (
    <div
      style={{
        display: "flex",
        alignItems: "center",
        justifyContent: "space-between",
        padding: "7px 0",
        borderBottom: "1px solid var(--glass-border)",
        fontSize: "13px",
      }}
    >
      <div style={{ display: "flex", alignItems: "center", gap: "8px" }}>
        <span
          style={{
            width: "7px",
            height: "7px",
            borderRadius: "50%",
            background: color,
            flexShrink: 0,
          }}
        />
        <span style={{ color: "var(--text-secondary)" }}>{label}</span>
      </div>
      <span style={{ fontWeight: 600, color }}>{value}</span>
    </div>
  );
}
