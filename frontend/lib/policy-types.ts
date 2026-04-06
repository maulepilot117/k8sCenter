/** Engine type matching backend policy.Engine */
export type Engine = "" | "kyverno" | "gatekeeper" | "both";

export interface EngineDetail {
  available: boolean;
  namespace?: string;
  webhooks: number;
}

export interface EngineStatus {
  detected: Engine;
  kyverno?: EngineDetail;
  gatekeeper?: EngineDetail;
  lastChecked: string;
}

export interface NormalizedPolicy {
  id: string;
  name: string;
  namespace?: string;
  kind: string;
  action: string;
  category?: string;
  severity: string;
  description?: string;
  nativeAction: string;
  engine: string;
  blocking: boolean;
  ready: boolean;
  ruleCount: number;
  violationCount: number;
  targetKinds?: string[];
}

export interface NormalizedViolation {
  policy: string;
  rule?: string;
  severity: string;
  action: string;
  message: string;
  namespace?: string;
  kind: string;
  name: string;
  timestamp?: string;
  engine: string;
  blocking: boolean;
}

export interface SeverityCounts {
  pass: number;
  fail: number;
  total: number;
}

export interface ComplianceScore {
  scope: string;
  score: number;
  pass: number;
  fail: number;
  warn: number;
  total: number;
  bySeverity?: Record<string, SeverityCounts>;
}
