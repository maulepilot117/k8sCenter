/** @jsxImportSource react */
interface TextFieldProps {
  value: string;
  onInput: (v: string) => void;
  placeholder?: string;
  /** monospace (image refs, ports, CPU/mem) */
  mono?: boolean;
  width?: string;
  /** input type; use "password" for secret fields (tokens, passwords) */
  type?: "text" | "password";
}

export default function TextField({
  value,
  onInput,
  placeholder,
  mono,
  width = "100%",
  type = "text",
}: TextFieldProps) {
  return (
    <input
      type={type}
      value={value}
      placeholder={placeholder}
      onChange={(e) => onInput(e.currentTarget.value)}
      onFocus={(e) => (e.currentTarget.style.borderColor = "var(--accent)")}
      onBlur={(e) =>
        (e.currentTarget.style.borderColor = "var(--border-subtle)")
      }
      style={{
        width,
        padding: "10px 12px",
        borderRadius: "9px",
        background: "var(--bg-elevated)",
        border: "1px solid var(--border-subtle)",
        color: "var(--text-primary)",
        fontSize: "13.5px",
        fontFamily: mono ? "var(--font-mono)" : "inherit",
        outline: "none",
      }}
    />
  );
}
