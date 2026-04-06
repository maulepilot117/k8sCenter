import { ColorBadge } from "@/components/ui/PolicyBadges.tsx";

export const SCANNER_COLORS: Record<string, string> = {
  trivy: "var(--accent)",
  kubescape: "var(--success)",
};

export const SEVERITY_COLORS: Record<string, string> = {
  critical: "var(--danger)",
  high: "var(--warning)",
  medium: "var(--accent)",
  low: "var(--text-muted)",
};

export function ScannerBadge({ scanner }: { scanner: string }) {
  const labels: Record<string, string> = {
    trivy: "Trivy",
    kubescape: "Kubescape",
    both: "Multi-scanner",
    "": "Unknown",
  };
  return (
    <ColorBadge
      label={labels[scanner] ?? scanner}
      color={SCANNER_COLORS[scanner] ?? "var(--text-muted)"}
    />
  );
}

export function SeverityCount(
  { label, count, color }: { label: string; count: number; color: string },
) {
  if (count === 0) return null;
  return (
    <span
      class="inline-flex items-center gap-1 px-2 py-0.5 rounded text-xs font-medium"
      style={{
        color,
        backgroundColor: `color-mix(in srgb, ${color} 15%, transparent)`,
      }}
    >
      <span class="font-bold">{count}</span>
      {label}
    </span>
  );
}
