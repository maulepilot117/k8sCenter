import WidgetShell from "@/components/ui/WidgetShell.tsx";
import { SparklineChart } from "@/components/ui/SparklineChart.tsx";
import { formatMbps } from "@/lib/format.ts";

export interface NetworkTileProps {
  /** 95th-percentile receive throughput over the period, in Mbps. */
  rxP95: number;
  /** 95th-percentile transmit throughput over the period, in Mbps. */
  txP95: number;
  rxData?: number[] | null;
  txData?: number[] | null;
  /** Time-range label (e.g. "1h") shown next to the "p95" qualifier. */
  period: string;
  href?: string;
}

function Row(
  { arrow, label, value, data, color }: {
    arrow: string;
    label: string;
    value: number;
    data?: number[] | null;
    color: string;
  },
) {
  return (
    <div
      style={{
        display: "flex",
        alignItems: "center",
        gap: "8px",
      }}
    >
      <span
        style={{
          fontSize: "11px",
          fontWeight: 600,
          color: "var(--text-muted)",
          minWidth: "38px",
          display: "flex",
          alignItems: "center",
          gap: "3px",
        }}
      >
        <span style={{ color }}>{arrow}</span>
        {label}
      </span>
      <span
        style={{
          fontSize: "20px",
          fontWeight: 700,
          letterSpacing: "-0.02em",
          color: "var(--text-primary)",
          fontFamily: "var(--font-sans)",
          minWidth: "42px",
          textAlign: "right",
        }}
      >
        {formatMbps(value)}
      </span>
      <span
        style={{
          fontSize: "11px",
          color: "var(--text-muted)",
          fontWeight: 500,
        }}
      >
        Mbps
      </span>
      <div style={{ flex: 1, minWidth: "40px" }}>
        {data && data.length >= 2 && (
          <SparklineChart data={data} color={color} height={22} />
        )}
      </div>
    </div>
  );
}

// NetworkTile renders cluster-wide network throughput as two distinct rows —
// receive (RX) and transmit (TX) — each showing its 95th-percentile value over
// the selected time window plus a sparkline. It replaces the former Alerts tile
// in the dashboard's 2×2 metric grid; critical-alert visibility lives on the
// Cluster Health card.
export function NetworkTile(
  { rxP95, txP95, rxData, txData, period, href }: NetworkTileProps,
) {
  const inner = (
    <WidgetShell padding={16}>
      <div
        style={{
          display: "flex",
          alignItems: "flex-start",
          justifyContent: "space-between",
          marginBottom: "8px",
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
          Network I/O
        </span>
        <span
          style={{
            fontSize: "11px",
            fontWeight: 500,
            color: "var(--text-muted)",
          }}
        >
          p95 · {period}
        </span>
      </div>
      <div style={{ display: "flex", flexDirection: "column", gap: "8px" }}>
        <Row
          arrow="▼"
          label="RX"
          value={rxP95}
          data={rxData}
          color="var(--accent)"
        />
        <Row
          arrow="▲"
          label="TX"
          value={txP95}
          data={txData}
          color="var(--accent-secondary)"
        />
      </div>
    </WidgetShell>
  );

  if (href) {
    return (
      <a
        href={href}
        style={{ textDecoration: "none", color: "inherit", display: "block" }}
      >
        {inner}
      </a>
    );
  }
  return inner;
}
