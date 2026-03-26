import { statusStyle } from"@/lib/status-colors.ts";
import type { StatusVariant } from"@/lib/status-colors.ts";

interface StatusBadgeProps {
 status: string;
 variant?: StatusVariant;
}

const VARIANT_STYLES: Record<StatusVariant, Record<string, string>> = {
 success: { background:"var(--success-dim)", color:"var(--success)" },
 warning: { background:"var(--warning-dim)", color:"var(--warning)" },
 danger: { background:"var(--error-dim)", color:"var(--error)" },
 info: { background:"var(--accent-dim)", color:"var(--accent)" },
 neutral: { background:"var(--bg-elevated)", color:"var(--text-muted)" },
};

export function StatusBadge({ status, variant }: StatusBadgeProps) {
 const styles = variant
 ? VARIANT_STYLES[variant]
 : statusStyle(status);

 return (
 <span
 class="inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium"
 style={styles}
 >
 {status}
 </span>
 );
}
