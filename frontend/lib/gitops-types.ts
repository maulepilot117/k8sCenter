/** Tool identifies which GitOps tool manages a resource. */
export type Tool = "" | "argocd" | "fluxcd" | "both";

export type SyncStatus =
  | "synced"
  | "outofsync"
  | "progressing"
  | "stalled"
  | "failed"
  | "unknown";

export type HealthStatus =
  | "healthy"
  | "degraded"
  | "progressing"
  | "suspended"
  | "unknown";

export interface GitOpsStatus {
  detected: Tool;
  lastChecked: string;
}

export interface AppSource {
  repoURL?: string;
  path?: string;
  targetRevision?: string;
  chartName?: string;
  chartVersion?: string;
}

export interface NormalizedApp {
  id: string;
  name: string;
  namespace: string;
  tool: Tool;
  kind: string;
  syncStatus: SyncStatus;
  healthStatus: HealthStatus;
  source: AppSource;
  currentRevision?: string;
  lastSyncTime?: string;
  message?: string;
  destinationCluster?: string;
  destinationNamespace?: string;
  managedResourceCount: number;
  suspended: boolean;
}

export interface ManagedResource {
  group?: string;
  kind: string;
  namespace?: string;
  name: string;
  status: string;
  health?: string;
}

export interface RevisionEntry {
  revision: string;
  status: string;
  message?: string;
  deployedAt: string;
}

export interface AppDetail {
  app: NormalizedApp;
  resources?: ManagedResource[];
  history?: RevisionEntry[];
}

export interface AppListMetadata {
  total: number;
  synced: number;
  outOfSync: number;
  degraded: number;
  progressing: number;
  suspended: number;
}

export interface AppListResponse {
  applications: NormalizedApp[];
  summary: AppListMetadata;
}
