/** Scanner identifies which security scanning tool produced a report. */
export type Scanner = "" | "trivy" | "kubescape" | "both";

export interface ScannerDetail {
  available: boolean;
  namespace?: string;
}

export interface ScannerStatus {
  detected: Scanner;
  trivy?: ScannerDetail;
  kubescape?: ScannerDetail;
  lastChecked: string;
}

export interface SeveritySummary {
  critical: number;
  high: number;
  medium: number;
  low: number;
}

export interface ImageVulnInfo {
  image: string;
  severities: SeveritySummary;
}

export interface WorkloadVulnSummary {
  namespace: string;
  kind: string;
  name: string;
  images: ImageVulnInfo[];
  total: SeveritySummary;
  lastScanned: string;
  scanner: Scanner;
}

export interface VulnListMetadata {
  total: number;
  severity: SeveritySummary;
}

export interface VulnListResponse {
  vulnerabilities: WorkloadVulnSummary[];
  summary: VulnListMetadata;
}
