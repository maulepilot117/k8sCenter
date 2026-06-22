/** iOS-style switch. Booleans in wizards/settings (expose, enable, etc.). */
export default function Toggle(
  { checked, onChange }: { checked: boolean; onChange: (v: boolean) => void },
) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      onClick={() => onChange(!checked)}
      style={{
        position: "relative",
        width: "38px",
        height: "22px",
        borderRadius: "11px",
        border: "none",
        cursor: "pointer",
        flexShrink: 0,
        transition: "background 150ms ease",
        background: checked ? "var(--accent)" : "var(--border-primary)",
      }}
    >
      <span
        style={{
          position: "absolute",
          top: "2px",
          width: "18px",
          height: "18px",
          borderRadius: "50%",
          background: "var(--text-on-accent)",
          transition: "left 150ms ease",
          left: checked ? "18px" : "2px",
        }}
      />
    </button>
  );
}
