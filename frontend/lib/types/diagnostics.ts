/** Shared types and constants for diagnostic islands. */

/** Maps Kubernetes kind names to URL route segments. */
export const KIND_ROUTE_MAP: Record<string, string> = {
  Pod: "pods",
  Deployment: "deployments",
  StatefulSet: "statefulsets",
  DaemonSet: "daemonsets",
  Service: "services",
  Job: "jobs",
  CronJob: "cronjobs",
  PersistentVolumeClaim: "pvcs",
  ReplicaSet: "replicasets",
  Ingress: "ingresses",
  ConfigMap: "configmaps",
  Secret: "secrets",
  Endpoints: "endpoints",
};

/** Maps Kubernetes kind names to their URL section (workloads, networking, etc). */
export function getResourceSection(kind: string): string {
  const networking = ["Service", "Ingress", "Endpoints"];
  const config = ["ConfigMap", "Secret"];
  const storage = ["PersistentVolumeClaim"];
  if (networking.includes(kind)) return "networking";
  if (config.includes(kind)) return "config";
  if (storage.includes(kind)) return "storage";
  return "workloads";
}

export interface DiagnosticResult {
  ruleName: string;
  status: "pass" | "warn" | "fail";
  severity: "critical" | "warning" | "info";
  message: string;
  detail?: string;
  remediation?: string;
  links?: { label: string; kind: string; name: string }[];
}

export interface AffectedResource {
  kind: string;
  name: string;
  health: string;
  impact: string;
}
