export default function Select(
  { value, options, onChange }: {
    value: string;
    options: string[];
    onChange: (v: string) => void;
  },
) {
  return (
    <select
      value={value}
      onChange={(e) => onChange((e.target as HTMLSelectElement).value)}
      style={{
        width: "100%",
        padding: "10px 12px",
        borderRadius: "9px",
        background: "var(--bg-elevated)",
        border: "1px solid var(--border-subtle)",
        color: "var(--text-primary)",
        fontSize: "13.5px",
        fontFamily: "inherit",
        outline: "none",
        cursor: "pointer",
      }}
    >
      {options.map((o) => <option key={o} value={o}>{o}</option>)}
    </select>
  );
}
