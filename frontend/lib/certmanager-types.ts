/** cert-manager types matching backend/internal/certmanager/types.go */

export type CertStatus =
  | "Ready"
  | "Issuing"
  | "Failed"
  | "Expiring"
  | "Expired"
  | "Unknown";

export interface IssuerRef {
  name: string;
  kind: string;
  group?: string;
}

/** Layer of the resolution chain that supplied a Certificate's
 * effective expiry thresholds. Drives the "Warns at: 60d (from
 * Issuer X)" tooltip on the Certificate detail page. */
export type ThresholdSource =
  | "default"
  | "certificate"
  | "issuer"
  | "clusterissuer";

export interface Certificate {
  name: string;
  namespace: string;
  status: CertStatus;
  reason?: string;
  message?: string;
  issuerRef: IssuerRef;
  secretName: string;
  dnsNames?: string[];
  commonName?: string;
  duration?: string;
  renewBefore?: string;
  notBefore?: string;
  notAfter?: string;
  renewalTime?: string;
  daysRemaining?: number;
  /** Effective per-cert thresholds resolved through cert > issuer >
   * clusterissuer > package-default. omitempty on the wire when the
   * resolver hasn't run; absent or zero means "not resolved yet". */
  warningThresholdDays?: number;
  criticalThresholdDays?: number;
  /** Aggregate source — strongest layer that contributed a value.
   * For the per-key attribution use warningThresholdSource /
   * criticalThresholdSource (each can come from a different layer). */
  thresholdSource?: ThresholdSource;
  /** Per-key source attribution. A cert can override warn alone and
   * inherit crit from its issuer; these fields tell the UI exactly
   * which layer produced each value. */
  warningThresholdSource?: ThresholdSource;
  criticalThresholdSource?: ThresholdSource;
  /** True when the resolved warn/crit pair would have violated
   * crit < warn. The resolver fell back to package defaults; the UI
   * surfaces this so operators see "Conflict — using defaults" rather
   * than a misleading "Default" badge despite their annotation. */
  thresholdConflict?: boolean;
  uid: string;
}

export interface Issuer {
  name: string;
  namespace?: string;
  scope: "Namespaced" | "Cluster";
  type: "ACME" | "CA" | "Vault" | "SelfSigned" | "Unknown";
  ready: boolean;
  reason?: string;
  message?: string;
  acmeEmail?: string;
  acmeServer?: string;
  /** Annotation-set threshold overrides on this Issuer. Inherited by
   * Certificates that reference it when those certs don't carry their
   * own annotation. omitempty when the issuer has no annotation. */
  warningThresholdDays?: number;
  criticalThresholdDays?: number;
  uid: string;
  updatedAt: string;
}

export interface CertificateRequest {
  name: string;
  namespace: string;
  status: CertStatus;
  reason?: string;
  message?: string;
  issuerRef: IssuerRef;
  createdAt: string;
  finishedAt?: string;
  uid: string;
}

export interface Order {
  name: string;
  namespace: string;
  state: string;
  reason?: string;
  createdAt: string;
  uid: string;
  crName?: string;
}

export interface Challenge {
  name: string;
  namespace: string;
  type: string;
  state: string;
  reason?: string;
  dnsName?: string;
  createdAt: string;
  uid: string;
  orderName?: string;
}

export interface CertificateDetail {
  certificate: Certificate;
  certificateRequests?: CertificateRequest[];
  orders?: Order[];
  challenges?: Challenge[];
}

export interface ExpiringCertificate {
  namespace: string;
  name: string;
  uid: string;
  issuerName: string;
  secretName: string;
  notAfter: string;
  daysRemaining: number;
  severity: "warning" | "critical" | "expired";
}

export interface CertManagerStatus {
  detected: boolean;
  namespace?: string;
  version?: string;
  lastChecked: string;
}
