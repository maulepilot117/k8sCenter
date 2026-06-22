import type { ComponentChildren } from "preact";

/** Label + optional hint wrapper used by every wizard/form field. */
export default function Field(
  { label, hint, children }: {
    label: string;
    hint?: string;
    children: ComponentChildren;
  },
) {
  return (
    <div>
      <label
        style={{
          display: "block",
          fontSize: "12.5px",
          fontWeight: 600,
          color: "var(--text-secondary)",
          marginBottom: "7px",
        }}
      >
        {label}
      </label>
      {children}
      {hint && (
        <div
          style={{
            fontSize: "11.5px",
            color: "var(--text-muted)",
            marginTop: "6px",
          }}
        >
          {hint}
        </div>
      )}
    </div>
  );
}
