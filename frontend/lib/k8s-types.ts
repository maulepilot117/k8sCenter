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

/** User info from /auth/me — matches backend auth.User struct. */
export interface UserInfo {
  id: string;
  username: string;
  provider: string;
  kubernetesUsername: string;
  kubernetesGroups: string[];
  roles: string[];
}

/** Auth provider info from /auth/providers — matches backend auth.ProviderInfo. */
export interface ProviderInfo {
  id: string;
  type: string;
  displayName: string;
  loginURL?: string;
}

// ---- Kubernetes resource types (matches client-go JSON serialization) ----

/** Common k8s ObjectMeta fields returned by the backend. */
export interface K8sMetadata {
  name: string;
  namespace?: string;
  uid: string;
  creationTimestamp: string;
  labels?: Record<string, string>;
  annotations?: Record<string, string>;
  resourceVersion?: string;
  ownerReferences?: Array<{
    apiVersion: string;
    kind: string;
    name: string;
    uid: string;
    controller?: boolean;
  }>;
  finalizers?: string[];
  deletionTimestamp?: string;
  managedFields?: unknown[];
}

/** Generic k8s resource with metadata — base for all resource types. */
export interface K8sResource {
  metadata: K8sMetadata;
  [key: string]: unknown;
}

// -- Pods --
export interface PodStatus {
  phase: string;
  conditions?: Array<{ type: string; status: string }>;
  containerStatuses?: Array<{
    name: string;
    ready: boolean;
    restartCount: number;
    state: Record<string, unknown>;
  }>;
}

export interface Pod extends K8sResource {
  spec: {
    nodeName?: string;
    containers: Array<{ name: string; image: string }>;
    restartPolicy?: string;
  };
  status?: PodStatus;
}

// -- Deployments --
export interface Deployment extends K8sResource {
  spec: {
    replicas?: number;
    selector: { matchLabels?: Record<string, string> };
    template?: {
      spec?: { containers?: Array<{ name: string; image: string }> };
    };
    strategy?: {
      type?: string;
      rollingUpdate?: { maxUnavailable?: unknown; maxSurge?: unknown };
    };
  };
  status?: {
    replicas?: number;
    readyReplicas?: number;
    availableReplicas?: number;
    updatedReplicas?: number;
    conditions?: Array<{
      type: string;
      status: string;
      reason?: string;
      message?: string;
      lastTransitionTime?: string;
    }>;
  };
}

// -- StatefulSets --
export interface StatefulSet extends K8sResource {
  spec: {
    replicas?: number;
    serviceName: string;
    selector: { matchLabels?: Record<string, string> };
    updateStrategy?: {
      type?: string;
      rollingUpdate?: { partition?: number };
    };
  };
  status?: {
    replicas?: number;
    readyReplicas?: number;
    currentReplicas?: number;
    updatedReplicas?: number;
  };
}

// -- DaemonSets --
export interface DaemonSet extends K8sResource {
  spec: {
    selector: { matchLabels?: Record<string, string> };
  };
  status?: {
    desiredNumberScheduled?: number;
    currentNumberScheduled?: number;
    numberReady?: number;
    numberAvailable?: number;
  };
}

// -- Services --
export interface Service extends K8sResource {
  spec: {
    type: string;
    clusterIP?: string;
    ports?: Array<
      {
        port: number;
        targetPort?: number | string;
        protocol?: string;
        name?: string;
      }
    >;
    selector?: Record<string, string>;
  };
}

// -- Ingresses --
export interface Ingress extends K8sResource {
  spec: {
    rules?: Array<{
      host?: string;
      http?: { paths: Array<{ path?: string; backend: unknown }> };
    }>;
    tls?: Array<{ hosts?: string[]; secretName?: string }>;
  };
  status?: {
    loadBalancer?: { ingress?: Array<{ ip?: string; hostname?: string }> };
  };
}

// -- ConfigMaps --
export interface ConfigMap extends K8sResource {
  data?: Record<string, string>;
}

// -- Secrets --
export interface Secret extends K8sResource {
  type?: string;
  data?: Record<string, string>;
}

// -- Namespaces --
export interface Namespace extends K8sResource {
  status?: { phase?: string };
}

// -- Nodes --
export interface Node extends K8sResource {
  spec: {
    unschedulable?: boolean;
    taints?: Array<{ key: string; value?: string; effect: string }>;
  };
  status: {
    conditions?: Array<{ type: string; status: string }>;
    nodeInfo?: {
      kubeletVersion: string;
      osImage: string;
      architecture: string;
      containerRuntimeVersion: string;
    };
    addresses?: Array<{ type: string; address: string }>;
    capacity?: Record<string, string>;
    allocatable?: Record<string, string>;
  };
}

// -- PVCs --
export interface PersistentVolumeClaim extends K8sResource {
  spec: {
    accessModes?: string[];
    storageClassName?: string;
    volumeName?: string;
    resources?: { requests?: Record<string, string> };
  };
  status?: {
    phase?: string;
    capacity?: Record<string, string>;
  };
}

// -- Jobs --
export interface Job extends K8sResource {
  spec: {
    completions?: number;
    parallelism?: number;
    backoffLimit?: number;
  };
  status: {
    active?: number;
    succeeded?: number;
    failed?: number;
    startTime?: string;
    completionTime?: string;
    conditions?: Array<{ type: string; status: string }>;
  };
}

// -- CronJobs --
export interface CronJob extends K8sResource {
  spec: {
    schedule: string;
    suspend?: boolean;
    concurrencyPolicy?: string;
    jobTemplate: unknown;
  };
  status?: {
    lastScheduleTime?: string;
    active?: Array<{ name: string }>;
  };
}

// -- NetworkPolicies --
export interface NetworkPolicy extends K8sResource {
  spec: {
    podSelector: { matchLabels?: Record<string, string> };
    policyTypes?: string[];
    ingress?: unknown[];
    egress?: unknown[];
  };
}

// -- RBAC types --
export interface Role extends K8sResource {
  rules?: Array<{
    apiGroups?: string[];
    resources?: string[];
    verbs?: string[];
  }>;
}

export interface ClusterRole extends K8sResource {
  rules?: Array<{
    apiGroups?: string[];
    resources?: string[];
    verbs?: string[];
  }>;
}

export interface RoleBinding extends K8sResource {
  subjects?: Array<{ kind: string; name: string; namespace?: string }>;
  roleRef: { kind: string; name: string; apiGroup: string };
}

export interface ClusterRoleBinding extends K8sResource {
  subjects?: Array<{ kind: string; name: string; namespace?: string }>;
  roleRef: { kind: string; name: string; apiGroup: string };
}

// -- Alerts --
export interface AlertEvent {
  id: string;
  fingerprint: string;
  status: string;
  alertName: string;
  namespace: string;
  severity: string;
  labels: Record<string, string>;
  annotations: Record<string, string>;
  startsAt: string;
  endsAt?: string;
  receivedAt: string;
  resolvedAt?: string;
}

// -- Events --
export interface K8sEvent extends K8sResource {
  type?: string;
  reason?: string;
  message?: string;
  involvedObject?: {
    kind: string;
    name: string;
    namespace?: string;
  };
  count?: number;
  firstTimestamp?: string;
  lastTimestamp?: string;
  source?: { component?: string };
}
