/** Standard API response envelope from the Go backend. */
export interface APIResponse<T> {
  data: T;
  metadata?: {
    total?: number;
    page?: number;
    pageSize?: number;
    continue?: string;
  };
}

/** Standard API error response from the Go backend. */
export interface APIError {
  error: {
    code: number;
    message: string;
    detail?: string;
  };
}

/** Auth login response. */
export interface LoginResponse {
  data: {
    accessToken: string;
    user: UserInfo;
  };
}

/** User info from /auth/me. */
export interface UserInfo {
  username: string;
  role: string;
  kubernetesUsername?: string;
}

/** Cluster info from /cluster/info. */
export interface ClusterInfo {
  version: string;
  nodeCount: number;
  kubecenterVersion: string;
}

/** Auth provider info from /auth/providers. */
export interface AuthProviders {
  providers: string[];
}

/** Common Kubernetes object metadata. */
export interface ObjectMeta {
  name: string;
  namespace?: string;
  uid: string;
  creationTimestamp: string;
  labels?: Record<string, string>;
  annotations?: Record<string, string>;
}

/** Kubernetes resource with metadata. */
export interface KubeResource {
  kind: string;
  apiVersion: string;
  metadata: ObjectMeta;
}

/** Pod status phase. */
export type PodPhase =
  | "Pending"
  | "Running"
  | "Succeeded"
  | "Failed"
  | "Unknown";

/** Node condition status. */
export type ConditionStatus = "True" | "False" | "Unknown";

/** Deployment summary for list views. */
export interface DeploymentSummary extends KubeResource {
  spec: {
    replicas: number;
  };
  status: {
    readyReplicas?: number;
    availableReplicas?: number;
    updatedReplicas?: number;
  };
}

/** Pod summary for list views. */
export interface PodSummary extends KubeResource {
  status: {
    phase: PodPhase;
    containerStatuses?: Array<{
      name: string;
      ready: boolean;
      restartCount: number;
    }>;
  };
  spec: {
    nodeName?: string;
  };
}

/** Node summary for list views. */
export interface NodeSummary extends KubeResource {
  status: {
    conditions: Array<{
      type: string;
      status: ConditionStatus;
    }>;
    allocatable: {
      cpu: string;
      memory: string;
    };
    capacity: {
      cpu: string;
      memory: string;
    };
  };
  metadata: ObjectMeta & {
    labels: Record<string, string>;
  };
}
