/** ThresholdStatus indicates how close a resource is to its quota. */
export type ThresholdStatus = "ok" | "warning" | "critical";

/** NamespaceSummary is the dashboard row for one namespace. */
export interface NamespaceSummary {
  namespace: string;
  hasQuota: boolean;
  hasLimitRange: boolean;
  cpuUsedPercent?: number;
  memoryUsedPercent?: number;
  highestUtilization: number;
  status: ThresholdStatus;
  quotaCount: number;
  limitRangeCount: number;
}

/** NamespaceLimits is the detail view for one namespace. */
export interface NamespaceLimits {
  namespace: string;
  quotas: NormalizedQuota[];
  limitRanges: NormalizedLimitRange[];
}

/** NormalizedQuota wraps a ResourceQuota with computed utilization. */
export interface NormalizedQuota {
  name: string;
  utilization: Record<string, ResourceUtilization>;
  warnThreshold: number;
  criticalThreshold: number;
}

/** ResourceUtilization tracks usage for one resource dimension. */
export interface ResourceUtilization {
  used: string;
  hard: string;
  percentage: number;
  status: ThresholdStatus;
}

/** NormalizedLimitRange abstracts away k8s API details. */
export interface NormalizedLimitRange {
  name: string;
  limits: LimitRangeItem[];
}

/** LimitRangeItem is one limit type within a LimitRange. */
export interface LimitRangeItem {
  type: "Container" | "Pod" | "PersistentVolumeClaim";
  default?: Record<string, string>;
  defaultRequest?: Record<string, string>;
  min?: Record<string, string>;
  max?: Record<string, string>;
  maxLimitRequestRatio?: Record<string, string>;
}

/** LimitsStatus is the status response from the limits API. */
export interface LimitsStatus {
  available: boolean;
}
