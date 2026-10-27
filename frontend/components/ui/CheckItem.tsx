export interface CheckItemProps {
  label: string;
  value: string;
  /**
   * `neutral` is for a value that was deliberately not evaluated -- a check
   * with nothing to check, such as alerting on a cluster with no Alertmanager.
   * It is neither a pass nor something to look at.
   */
  status: "success" | "warning" | "error" | "neutral";
  /** Where the row leads: the list the value was counted from. */
  href?: string;
}

const STATUS_COLOR: Record<CheckItemProps["status"], string> = {
  success: "var(--success)",
  warning: "var(--warning)",
  error: "var(--error)",
  neutral: "var(--text-muted)",
};

const ROW_STYLE = {
  display: "flex",
  alignItems: "center",
  justifyContent: "space-between",
  padding: "7px 0",
  borderBottom: "1px solid var(--glass-border)",
  fontSize: "13px",
};

export function CheckItem({ label, value, status, href }: CheckItemProps) {
  const color = STATUS_COLOR[status];
  const content = (
    <>
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
    </>
  );

  if (href) {
    return (
      <a
        href={href}
        class="hover:underline"
        style={{ ...ROW_STYLE, color: "inherit" }}
      >
        {content}
      </a>
    );
  }
  return <div style={ROW_STYLE}>{content}</div>;
}
