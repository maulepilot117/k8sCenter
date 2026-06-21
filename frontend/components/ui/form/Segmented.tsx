interface SegmentedProps {
  value: string;
  options: string[];
  onChange: (v: string) => void;
}

/** Inline segmented control for 2–3 short choices (e.g. Service type). */
export default function Segmented(
  { value, options, onChange }: SegmentedProps,
) {
  return (
    <div
      style={{
        display: "flex",
        gap: "6px",
        padding: "4px",
        borderRadius: "11px",
        background: "var(--bg-elevated)",
        border: "1px solid var(--border-subtle)",
      }}
    >
      {options.map((o) => {
        const sel = o === value;
        return (
          <button
            key={o}
            type="button"
            onClick={() => onChange(o)}
            style={{
              flex: 1,
              padding: "9px 8px",
              borderRadius: "8px",
              border: "none",
              cursor: "pointer",
              fontSize: "12.5px",
              fontWeight: 600,
              fontFamily: "inherit",
              transition: "all 120ms ease",
              background: sel ? "var(--accent)" : "transparent",
              color: sel ? "#fff" : "var(--text-secondary)",
            }}
          >
            {o}
          </button>
        );
      })}
    </div>
  );
}
