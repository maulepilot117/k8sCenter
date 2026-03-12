/**
 * Format a Kubernetes timestamp into a human-readable relative age.
 * e.g., "5m", "2h", "3d"
 */
export function formatAge(timestamp: string): string {
  const now = Date.now();
  const then = new Date(timestamp).getTime();
  const diffMs = now - then;

  if (diffMs < 0) return "0s";

  const seconds = Math.floor(diffMs / 1000);
  if (seconds < 60) return `${seconds}s`;

  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m`;

  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h`;

  const days = Math.floor(hours / 24);
  return `${days}d`;
}

/**
 * Format bytes into human-readable size.
 * e.g., "1.5 GiB", "256 MiB"
 */
export function formatBytes(bytes: number): string {
  if (bytes === 0) return "0 B";

  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  const i = Math.floor(Math.log(bytes) / Math.log(1024));
  const value = bytes / Math.pow(1024, i);

  return `${value >= 10 ? Math.round(value) : value.toFixed(1)} ${units[i]}`;
}

/**
 * Parse Kubernetes memory string (e.g., "1Gi", "256Mi", "1024Ki") to bytes.
 */
export function parseK8sMemory(mem: string): number {
  const match = mem.match(
    /^(\d+(?:\.\d+)?)\s*(Ki|Mi|Gi|Ti|Pi|Ei|k|M|G|T|P|E)?$/,
  );
  if (!match) return 0;

  const value = parseFloat(match[1]);
  const unit = match[2];

  const multipliers: Record<string, number> = {
    Ki: 1024,
    Mi: 1024 ** 2,
    Gi: 1024 ** 3,
    Ti: 1024 ** 4,
    Pi: 1024 ** 5,
    Ei: 1024 ** 6,
    k: 1000,
    M: 1000 ** 2,
    G: 1000 ** 3,
    T: 1000 ** 4,
    P: 1000 ** 5,
    E: 1000 ** 6,
  };

  return value * (unit ? multipliers[unit] : 1);
}

/**
 * Parse Kubernetes CPU string (e.g., "500m", "2") to millicores.
 */
export function parseK8sCpu(cpu: string): number {
  if (cpu.endsWith("m")) {
    return parseInt(cpu.slice(0, -1), 10);
  }
  return parseFloat(cpu) * 1000;
}

/**
 * Format CPU millicores for display.
 */
export function formatCpu(millicores: number): string {
  if (millicores >= 1000) {
    return `${(millicores / 1000).toFixed(1)} cores`;
  }
  return `${millicores}m`;
}

/**
 * Truncate a string with ellipsis.
 */
export function truncate(str: string, maxLength: number): string {
  if (str.length <= maxLength) return str;
  return str.slice(0, maxLength - 1) + "\u2026";
}
