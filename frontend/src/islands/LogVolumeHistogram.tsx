/** @jsxImportSource react */
import type { VolumeEntry } from "@/lib/types/logs.ts";
import { useIsClient } from "@/src/lib/use-is-client.ts";

interface LogVolumeHistogramProps {
  data: VolumeEntry[] | null;
}

export default function LogVolumeHistogram(props: LogVolumeHistogramProps) {
  const isClient = useIsClient();
  if (!isClient) return null;

  const entries = props.data;
  if (!entries || entries.length === 0) return null;

  // Aggregate all values across entries into time buckets (O(n) via Map)
  const bucketMap = new Map<number, number>();
  for (const entry of entries) {
    for (const [ts, countStr] of entry.values) {
      const count = parseInt(String(countStr), 10) || 0;
      bucketMap.set(ts, (bucketMap.get(ts) ?? 0) + count);
    }
  }
  const buckets = [...bucketMap.entries()]
    .map(([ts, count]) => ({ ts, count }))
    .sort((a, b) => a.ts - b.ts);

  if (buckets.length === 0) return null;

  const maxCount = Math.max(...buckets.map((b) => b.count), 1);
  const total = buckets.reduce((sum, b) => sum + b.count, 0);

  return (
    <div className="flex items-center gap-2 rounded-lg border border-border-primary bg-surface px-4 py-2">
      <span className="text-xs text-text-muted">Volume</span>
      <div className="flex flex-1 items-end gap-px" style={{ height: "24px" }}>
        {buckets.map((bucket, i) => {
          const height = Math.max((bucket.count / maxCount) * 100, 4);
          return (
            <div
              // biome-ignore lint/suspicious/noArrayIndexKey: buckets are replaced whole on each search and carry no state
              key={i}
              className="flex-1 rounded-t bg-accent-dim hover:bg-accent/50 transition-colors"
              style={{ height: `${height}%` }}
              title={`${bucket.count} entries`}
            />
          );
        })}
      </div>
      <span className="text-xs text-text-muted">
        {total.toLocaleString()} lines
      </span>
    </div>
  );
}
