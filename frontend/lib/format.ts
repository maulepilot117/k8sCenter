/** Shared formatting utilities for display across the UI. */

/** Formats a timestamp as a human-readable relative age string (e.g. "5m", "3d"). */
export function age(timestamp: string): string {
  const diff = Date.now() - new Date(timestamp).getTime();
  const secs = Math.floor(diff / 1000);
  if (secs < 60) return `${secs}s`;
  const mins = Math.floor(secs / 60);
  if (mins < 60) return `${mins}m`;
  const hours = Math.floor(mins / 60);
  if (hours < 24) return `${hours}h`;
  const days = Math.floor(hours / 24);
  return `${days}d`;
}

/**
 * Linear-interpolated percentile (`p` in 0..100) over a numeric series.
 * Non-finite samples are dropped first; returns 0 for an empty or
 * all-non-finite series, and the sole value for a single-element series.
 */
export function percentile(
  series: number[] | null | undefined,
  p: number,
): number {
  const clean = (series ?? []).filter((v) => Number.isFinite(v));
  if (clean.length === 0) return 0;
  if (clean.length === 1) return clean[0];
  const sorted = [...clean].sort((a, b) => a - b);
  const rank = (p / 100) * (sorted.length - 1);
  const lo = Math.floor(rank);
  const hi = Math.ceil(rank);
  if (lo === hi) return sorted[lo];
  return sorted[lo] + (sorted[hi] - sorted[lo]) * (rank - lo);
}

/**
 * Formats a Mbps throughput value: integers at/above 100 read cleanly without
 * noise decimals; below that, one decimal preserves resolution on quiet
 * clusters. A non-finite or absent value renders as an em-dash, not "NaN".
 */
export function formatMbps(v: number): string {
  if (!Number.isFinite(v)) return "—";
  if (v >= 100) return Math.round(v).toString();
  return (Math.round(v * 10) / 10).toString();
}
