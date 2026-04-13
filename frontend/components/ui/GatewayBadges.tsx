export function ProtocolBadge({ protocol }: { protocol: string }) {
  const colorClass = protocol === "HTTPS" || protocol === "TLS"
    ? "text-success bg-success/10"
    : protocol === "HTTP"
    ? "text-brand bg-brand/10"
    : "text-text-secondary bg-surface";

  return (
    <span
      class={`inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium ${colorClass}`}
    >
      {protocol}
    </span>
  );
}
