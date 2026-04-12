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

export interface Certificate {
  name: string;
  namespace: string;
  status: CertStatus;
  reason?: string;
  message?: string;
  issuerRef: IssuerRef;
  secretName: string;
  dnsNames?: string[];
  ipAddresses?: string[];
  uris?: string[];
  commonName?: string;
  duration?: string;
  renewBefore?: string;
  notBefore?: string;
  notAfter?: string;
  renewalTime?: string;
  daysRemaining?: number;
  uid: string;
  labels?: Record<string, string>;
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
  url?: string;
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
  token?: string;
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
