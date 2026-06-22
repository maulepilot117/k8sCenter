import WidgetShell from "@/components/ui/WidgetShell.tsx";

export interface KpiTileProps {
  label: string;
  value: number;
  color: string;
  href?: string;
}

export function KpiTile({ label, value, color, href }: KpiTileProps) {
  const inner = (
    <WidgetShell style={{ flex: "1 1 140px", minWidth: "120px" }}>
      <div
        style={{
          fontSize: "24px",
          fontWeight: 700,
          fontVariantNumeric: "tabular-nums",
          color,
          marginBottom: "4px",
        }}
      >
        {value}
      </div>
      <div
        style={{
          fontSize: "11px",
          fontWeight: 600,
          letterSpacing: "0.05em",
          textTransform: "uppercase",
          color: "var(--text-muted)",
        }}
      >
        {label}
      </div>
    </WidgetShell>
  );

  return href
    ? (
      <a href={href} style={{ display: "contents", textDecoration: "none" }}>
        {inner}
      </a>
    )
    : inner;
}
