import { SparklineChart } from"@/components/ui/SparklineChart.tsx";
import { StatusDot } from"@/components/ui/StatusDot.tsx";

interface MetricCardProps {
 value: number | string;
 label: string;
 status:"success" |"warning" |"error" |"info";
 statusText: string;
 sparklineData?: number[];
 sparklineColor?: string;
 href?: string;
}

const STATUS_PILL_STYLES: Record<
 MetricCardProps["status"],
 { bg: string; color: string }
> = {
 success: { bg:"var(--success-dim)", color:"var(--success)" },
 warning: { bg:"var(--warning-dim)", color:"var(--warning)" },
 error: { bg:"var(--error-dim)", color:"var(--error)" },
 info: { bg:"var(--accent-dim)", color:"var(--accent)" },
};

function MetricCardInner(
 { value, label, status, statusText, sparklineData, sparklineColor }:
 MetricCardProps,
) {
 const pill = STATUS_PILL_STYLES[status];

 return (
 <div
 style={{
 background:"var(--bg-surface)",
 border:"1px solid var(--border-primary)",
 borderRadius:"var(--radius)",
 padding:"16px",
 display:"flex",
 flexDirection:"column",
 gap:"8px",
 transition:"border-color 0.15s ease",
 }}
 >
 {/* Top row: status dot + badge pill */}
 <div
 style={{
 display:"flex",
 alignItems:"center",
 justifyContent:"space-between",
 }}
 >
 <StatusDot status={status} size={8} />
 <span
 style={{
 fontSize:"11px",
 fontWeight: 500,
 padding:"2px 8px",
 borderRadius:"9999px",
 background: pill.bg,
 color: pill.color,
 }}
 >
 {statusText}
 </span>
 </div>

 {/* Value */}
 <div
 style={{
 fontSize:"28px",
 fontFamily:"var(--font-mono, monospace)",
 fontWeight: 700,
 color:"var(--text-primary)",
 lineHeight: 1.1,
 }}
 >
 {value}
 </div>

 {/* Label */}
 <div
 style={{
 fontSize:"12px",
 color:"var(--text-muted)",
 }}
 >
 {label}
 </div>

 {/* Sparkline */}
 {sparklineData && sparklineData.length >= 2 && (
 <div style={{ marginTop:"4px" }}>
 <SparklineChart
 data={sparklineData}
 color={sparklineColor ??"var(--accent)"}
 width={120}
 height={28}
 />
 </div>
 )}
 </div>
 );
}

export default function MetricCard(props: MetricCardProps) {
 if (props.href) {
 return (
 <a
 href={props.href}
 style={{ textDecoration:"none", color:"inherit", display:"block" }}
 >
 <MetricCardInner {...props} />
 </a>
 );
 }
 return <MetricCardInner {...props} />;
}
