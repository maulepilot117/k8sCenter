/**
 * Shared form styles used by SchemaForm and SchemaFormField islands.
 */

export const inputStyle: Record<string, string> = {
  width: "100%",
  background: "var(--bg-surface)",
  border: "1px solid var(--border-primary)",
  borderRadius: "6px",
  padding: "8px 12px",
  color: "var(--text-primary)",
  fontSize: "13px",
  fontFamily: "var(--font-mono)",
  outline: "none",
  boxSizing: "border-box",
};

export const selectStyle: Record<string, string> = {
  ...inputStyle,
  appearance: "auto",
};

export const labelStyle: Record<string, string | number> = {
  display: "block",
  fontSize: "12px",
  fontWeight: 500,
  color: "var(--text-secondary)",
  marginBottom: "4px",
};
