import { type Signal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";

interface VolumeEntry {
  metric: Record<string, string>;
  values: [number, string][];
}

interface LogVolumeHistogramProps {
  data: Signal<VolumeEntry[] | null>;
  onBucketClick?: (start: string, end: string) => void;
}

export default function LogVolumeHistogram(props: LogVolumeHistogramProps) {
  if (!IS_BROWSER) return null;

  const entries = props.data.value;
  if (!entries || entries.length === 0) return null;

  // Aggregate all values across entries into time buckets
  const buckets: { ts: number; count: number }[] = [];
  for (const entry of entries) {
    for (const [ts, countStr] of entry.values) {
      const existing = buckets.find((b) => b.ts === ts);
      const count = parseInt(countStr) || 0;
      if (existing) {
        existing.count += count;
      } else {
        buckets.push({ ts, count });
      }
    }
  }
  buckets.sort((a, b) => a.ts - b.ts);

  if (buckets.length === 0) return null;

  const maxCount = Math.max(...buckets.map((b) => b.count), 1);
  const total = buckets.reduce((sum, b) => sum + b.count, 0);

  return (
    <div class="flex items-center gap-2 rounded-lg border border-border-primary bg-bg-surface px-4 py-2">
      <span class="text-xs text-text-muted">Volume</span>
      <div class="flex flex-1 items-end gap-px" style={{ height: "24px" }}>
        {buckets.map((bucket, i) => {
          const height = Math.max((bucket.count / maxCount) * 100, 4);
          return (
            <div
              key={i}
              class="flex-1 rounded-t cursor-pointer bg-accent-dim hover:bg-accent-primary/50 transition-colors"
              style={{ height: `${height}%` }}
              title={`${bucket.count} entries`}
              onClick={() => {
                if (props.onBucketClick && i < buckets.length - 1) {
                  props.onBucketClick(
                    new Date(bucket.ts * 1000).toISOString(),
                    new Date(buckets[i + 1].ts * 1000).toISOString()
                  );
                }
              }}
            />
          );
        })}
      </div>
      <span class="text-xs text-text-muted">{total.toLocaleString()} lines</span>
    </div>
  );
}
