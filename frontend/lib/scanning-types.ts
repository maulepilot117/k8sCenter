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

// --- Vulnerability detail view (per-workload CVEs) ---

export type Severity = "CRITICAL" | "HIGH" | "MEDIUM" | "LOW" | "UNKNOWN";

/** An individual CVE finding with full metadata. */
export interface CVEDetail {
  id: string;
  severity: Severity;
  /** CVSS v3 score (0-10). `null` when unavailable. */
  cvssScore: number | null;
  package: string;
  installedVersion: string;
  /** Empty string = no fix available. */
  fixedVersion: string;
  title: string;
  primaryLink: string;
}

/** Detailed vulnerabilities for a single container image. */
export interface ImageVulnDetail {
  name: string;
  container: string;
  vulnerabilities: CVEDetail[];
}

/** Full detail response for a workload. Summary counts are computed client-side. */
export interface WorkloadVulnDetail {
  namespace: string;
  kind: string;
  name: string;
  scanner: Scanner;
  lastScanned: string;
  images: ImageVulnDetail[];
}

/** Aggregated counts computed from the vulnerabilities array. */
export interface VulnDetailSummary {
  critical: number;
  high: number;
  medium: number;
  low: number;
  unknown: number;
  fixable: number;
  total: number;
}

/** Compute severity and fix-availability counts from an images array. */
export function computeVulnSummary(
  images: ImageVulnDetail[],
): VulnDetailSummary {
  const s: VulnDetailSummary = {
    critical: 0,
    high: 0,
    medium: 0,
    low: 0,
    unknown: 0,
    fixable: 0,
    total: 0,
  };
  for (const img of images) {
    for (const v of img.vulnerabilities) {
      s.total++;
      switch (v.severity) {
        case "CRITICAL":
          s.critical++;
          break;
        case "HIGH":
          s.high++;
          break;
        case "MEDIUM":
          s.medium++;
          break;
        case "LOW":
          s.low++;
          break;
        default:
          s.unknown++;
      }
      if (v.fixedVersion) s.fixable++;
    }
  }
  return s;
}
