interface StepperProps {
  value: number;
  min?: number;
  max?: number;
  onChange: (v: number) => void;
}

/** Numeric +/- stepper (replicas, ports, counts). */
export default function Stepper(
  { value, min = 1, max = 99, onChange }: StepperProps,
) {
  const btn = {
    width: "42px",
    height: "40px",
    border: "none",
    cursor: "pointer",
    background: "var(--bg-elevated)",
    color: "var(--text-secondary)",
    fontSize: "18px",
  } as const;
  return (
    <div
      style={{
        display: "flex",
        alignItems: "center",
        width: "140px",
        borderRadius: "9px",
        overflow: "hidden",
        border: "1px solid var(--border-subtle)",
      }}
    >
      <button
        type="button"
        style={btn}
        onClick={() => onChange(Math.max(min, value - 1))}
      >
        −
      </button>
      <span
        style={{
          flex: 1,
          textAlign: "center",
          fontSize: "15px",
          fontWeight: 650,
          background: "var(--bg-surface)",
          height: "40px",
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          color: "var(--text-primary)",
        }}
      >
        {value}
      </span>
      <button
        type="button"
        style={btn}
        onClick={() => onChange(Math.min(max, value + 1))}
      >
        +
      </button>
    </div>
  );
}
