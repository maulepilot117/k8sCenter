/** Compute a human-friendly relative time string. */
export function timeAgo(dateStr: string): string {
  const ms = Date.now() - new Date(dateStr).getTime();
  if (ms < 0) return "just now";
  const days = Math.floor(ms / 86400000);
  if (days > 0) return `${days}d ago`;
  const hours = Math.floor(ms / 3600000);
  if (hours > 0) return `${hours}h ago`;
  const mins = Math.floor(ms / 60000);
  if (mins > 0) return `${mins}m ago`;
  return "just now";
}
