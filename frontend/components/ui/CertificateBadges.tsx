import type { CertStatus, Issuer } from "@/lib/certmanager-types.ts";

/** Status color map for certificate lifecycle states. */
const STATUS_COLORS: Record<CertStatus, string> = {
  Ready: "var(--success)",
  Issuing: "var(--accent)",
  Failed: "var(--danger)",
  Expired: "var(--danger)",
  Expiring: "var(--warning)",
  Unknown: "var(--text-muted)",
};

/** Issuer type color map. */
const ISSUER_TYPE_COLORS: Record<Issuer["type"], string> = {
  ACME: "var(--accent)",
  CA: "var(--success)",
  Vault: "var(--warning)",
  SelfSigned: "var(--text-muted)",
  Unknown: "var(--text-muted)",
};

/** Generic tinted badge — color text on a color-mix background. */
function CertBadge({ label, color }: { label: string; color: string }) {
  return (
    <span
      class="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium"
      style={{
        color,
        backgroundColor: `color-mix(in srgb, ${color} 15%, transparent)`,
      }}
    >
      {label}
    </span>
  );
}

/** Renders a pill badge for certificate status. */
export function StatusBadge({ status }: { status: CertStatus }) {
  return (
    <CertBadge
      label={status}
      color={STATUS_COLORS[status] ?? "var(--text-muted)"}
    />
  );
}

/** Renders a pill badge for issuer type (ACME, CA, Vault, etc.). */
export function IssuerTypeBadge({ type }: { type: Issuer["type"] }) {
  return (
    <CertBadge
      label={type}
      color={ISSUER_TYPE_COLORS[type] ?? "var(--text-muted)"}
    />
  );
}

/** Renders a badge showing days remaining until certificate expiry. */
export function ExpiryBadge(
  { daysRemaining }: { daysRemaining?: number },
) {
  if (daysRemaining === undefined || daysRemaining === null) {
    return <span class="text-xs text-text-muted">&mdash;</span>;
  }
  if (daysRemaining < 0) {
    return <CertBadge label="Expired" color="var(--danger)" />;
  }
  if (daysRemaining <= 7) {
    return <CertBadge label={`${daysRemaining}d left`} color="var(--danger)" />;
  }
  if (daysRemaining <= 30) {
    return (
      <CertBadge label={`${daysRemaining}d left`} color="var(--warning)" />
    );
  }
  return <CertBadge label={`${daysRemaining}d`} color="var(--text-muted)" />;
}
