import { useContext } from "preact/hooks";
import { CellFillContext } from "@/components/dashboard/cell-fill.ts";
import { SparklineChart } from "@/components/ui/SparklineChart.tsx";
import WidgetShell from "@/components/ui/WidgetShell.tsx";

export interface MetricTileProps {
  label: string;
  value: string;
  unit?: string;
  delta?: number | null; // positive = up, negative = down
  sparkData?: number[] | null;
  sparkColor?: string;
  href?: string;
}

export function MetricTile({
  label,
  value,
  unit,
  delta,
  sparkData,
  sparkColor = "var(--accent)",
  href,
}: MetricTileProps) {
  const fill = useContext(CellFillContext);
  const inner = (
    <WidgetShell padding={16}>
      <div
        style={{
          display: "flex",
          alignItems: "flex-start",
          justifyContent: "space-between",
          marginBottom: "6px",
        }}
      >
        <span
          style={{
            fontSize: "11px",
            fontWeight: 600,
            textTransform: "uppercase",
            letterSpacing: "0.08em",
            color: "var(--text-muted)",
          }}
        >
          {label}
        </span>
        {delta !== undefined && delta !== null && (
          <span
            style={{
              fontSize: "11px",
              fontWeight: 500,
              color: delta >= 0 ? "var(--success)" : "var(--error)",
              display: "flex",
              alignItems: "center",
              gap: "2px",
            }}
          >
            {delta >= 0 ? "▲" : "▼"}
            {Math.abs(delta)}%
          </span>
        )}
      </div>
      <div
        style={{
          display: "flex",
          alignItems: "baseline",
          gap: "4px",
          lineHeight: 1,
        }}
      >
        <span
          style={{
            fontSize: "28px",
            fontWeight: 750,
            letterSpacing: "-0.02em",
            color: "var(--text-primary)",
            fontFamily: "var(--font-sans)",
          }}
        >
          {value}
        </span>
        {unit && (
          <span
            style={{
              fontSize: "13px",
              color: "var(--text-muted)",
              fontWeight: 500,
            }}
          >
            {unit}
          </span>
        )}
      </div>
      {sparkData && sparkData.length >= 2 && (
        <div style={{ marginTop: "10px" }}>
          <SparklineChart data={sparkData} color={sparkColor} height={30} />
        </div>
      )}
    </WidgetShell>
  );

  if (href) {
    return (
      <a
        href={href}
        style={{
          textDecoration: "none",
          color: "inherit",
          display: "block",
          // Passes a dashboard grid cell's height through to the card, which
          // fills it (see CellFillContext); content-height everywhere else.
          ...(fill ? { height: "100%" } : {}),
        }}
      >
        {inner}
      </a>
    );
  }
  return inner;
}
