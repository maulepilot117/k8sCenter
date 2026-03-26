interface StatusDotProps {
 status:"success" |"warning" |"error" |"info" |"neutral";
 pulse?: boolean;
 size?: number;
}

const STATUS_COLORS: Record<StatusDotProps["status"], string> = {
 success:"var(--success)",
 warning:"var(--warning)",
 error:"var(--error)",
 info:"var(--accent)",
 neutral:"var(--text-muted)",
};

export function StatusDot({ status, pulse = false, size = 8 }: StatusDotProps) {
 return (
 <span
 class={`inline-block rounded-full ${pulse ?"animate-pulse-glow" :""}`}
 style={{
 width: `${size}px`,
 height: `${size}px`,
 background: STATUS_COLORS[status],
 }}
 />
 );
}
