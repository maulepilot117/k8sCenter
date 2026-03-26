/**
 * Animated loading skeleton placeholder.
 * Use to show content shape while data is loading.
 *
 * @example
 * <Skeleton class="h-4 w-32" /> // single line
 * <Skeleton class="h-8 w-full" /> // full-width bar
 * <Skeleton class="h-24 w-full rounded-lg" /> // card placeholder
 */
export function Skeleton(
 { class: className ="" }: { class?: string },
) {
 return (
 <div
 class={`animate-pulse rounded ${className}`}
 style={{ background:"var(--bg-elevated)" }}
 />
 );
}

/**
 * Table skeleton — renders rows of placeholder content matching a table layout.
 */
export function TableSkeleton(
 { rows = 5, cols = 4 }: { rows?: number; cols?: number },
) {
 return (
 <div class="space-y-2">
 {/* Header */}
 <div class="flex gap-4 px-3 py-2">
 {Array.from({ length: cols }).map((_, i) => (
 <Skeleton key={`h-${i}`} class="h-4 flex-1" />
 ))}
 </div>
 {/* Rows */}
 {Array.from({ length: rows }).map((_, r) => (
 <div key={`r-${r}`} class="flex gap-4 px-3 py-3">
 {Array.from({ length: cols }).map((_, c) => (
 <Skeleton
 key={`${r}-${c}`}
 class={`h-4 ${c === 0 ?"w-40" :"flex-1"}`}
 />
 ))}
 </div>
 ))}
 </div>
 );
}
