import type { CSSProperties } from "react";

/**
 * Animated loading skeleton placeholder.
 * Use to show content shape while data is loading.
 *
 * @example
 * <Skeleton className="h-4 w-32" /> // single line
 * <Skeleton className="h-8 w-full" /> // full-width bar
 * <Skeleton className="h-24 w-full rounded-lg" /> // card placeholder
 * <Skeleton className="rounded-lg" style={{ gridColumn: "span 6" }} /> // grid cell
 */
export function Skeleton({
  className = "",
  style,
}: {
  className?: string;
  style?: CSSProperties;
}) {
  return (
    <div
      className={`animate-pulse rounded ${className}`}
      style={{ background: "var(--bg-elevated)", ...style }}
    />
  );
}

function range(n: number): number[] {
  const out: number[] = [];
  for (let i = 0; i < n; i++) out.push(i);
  return out;
}

/**
 * Table skeleton — renders rows of placeholder content matching a table layout.
 */
export function TableSkeleton({
  rows = 5,
  cols = 4,
}: {
  rows?: number;
  cols?: number;
}) {
  return (
    <div className="space-y-2">
      {/* Header */}
      <div className="flex gap-4 px-3 py-2">
        {range(cols).map((i) => (
          <Skeleton key={`h-${i}`} className="h-4 flex-1" />
        ))}
      </div>
      {/* Rows */}
      {range(rows).map((r) => (
        <div key={`r-${r}`} className="flex gap-4 px-3 py-3">
          {range(cols).map((c) => (
            <Skeleton
              key={`${r}-${c}`}
              className={`h-4 ${c === 0 ? "w-40" : "flex-1"}`}
            />
          ))}
        </div>
      ))}
    </div>
  );
}
