interface FilterChipProps {
 label: string;
 active?: boolean;
 onClick?: () => void;
}

export function FilterChip(
 { label, active = false, onClick }: FilterChipProps,
) {
 return (
 <button
 type="button"
 onClick={onClick}
 class="inline-flex items-center rounded-full px-3 py-1 text-xs font-medium border transition-colors"
 style={active
 ? {
 background:"var(--accent-dim)",
 borderColor:"var(--accent)",
 color:"var(--accent)",
 }
 : {
 background:"var(--bg-elevated)",
 borderColor:"var(--border-primary)",
 color:"var(--text-secondary)",
 }}
 >
 {label}
 </button>
 );
}
