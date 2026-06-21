interface BarRowProps {
  label: string;
  /** current value */
  value: number;
  max?: number;
  /** right-aligned value text, e.g. "4.2 cores" */
  suffix?: string;
  color?: string;
  labelWidth?: number;
}

/** Labeled horizontal progress bar. Namespace usage, node capacity, quotas. */
export default function BarRow(
  { label, value, max = 100, suffix, color = "var(--accent)", labelWidth = 96 }:
    BarRowProps,
) {
  const pct = Math.max(0, Math.min(100, (value / max) * 100));
  return (
    <div
      style={{
        display: "flex",
        alignItems: "center",
        gap: "12px",
        marginBottom: "12px",
      }}
    >
      <span
        style={{
          width: `${labelWidth}px`,
          fontSize: "13px",
          fontWeight: 600,
          whiteSpace: "nowrap",
          overflow: "hidden",
          textOverflow: "ellipsis",
          color: "var(--text-primary)",
        }}
      >
        {label}
      </span>
      <div
        style={{
          flex: 1,
          height: "8px",
          borderRadius: "4px",
          background: "var(--bg-hover)",
          overflow: "hidden",
        }}
      >
        <div
          style={{
            height: "100%",
            borderRadius: "4px",
            background: color,
            width: `${pct}%`,
          }}
        />
      </div>
      {suffix && (
        <span
          style={{
            width: "56px",
            fontSize: "12px",
            color: "var(--text-secondary)",
            textAlign: "right",
          }}
        >
          {suffix}
        </span>
      )}
    </div>
  );
}
