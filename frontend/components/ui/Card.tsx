import type { ComponentChildren } from"preact";

interface CardProps {
 title?: string;
 children: ComponentChildren;
 class?: string;
}

export function Card({ title, children, class: className }: CardProps) {
 return (
 <div
 class={`rounded-lg border p-6 ${className ??""}`}
 style={{
 background:"var(--bg-surface)",
 borderColor:"var(--border-primary)",
 }}
 >
 {title && (
 <h3
 class="mb-4 text-sm font-semibold uppercase tracking-wider"
 style={{ color:"var(--text-muted)" }}
 >
 {title}
 </h3>
 )}
 {children}
 </div>
 );
}
