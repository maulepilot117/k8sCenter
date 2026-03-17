/** Backend API base URL. In dev, the Fresh BFF proxy forwards to this. */
export const BACKEND_URL = typeof Deno !== "undefined"
  ? Deno.env.get("BACKEND_URL") ?? "http://localhost:8080"
  : "http://localhost:8080";

/**
 * Maps lowercase plural API kind to PascalCase Kubernetes API kind.
 * Used for event filtering (involvedObject.kind uses PascalCase).
 */
export const RESOURCE_API_KINDS: Record<string, string> = {
  pods: "Pod",
  deployments: "Deployment",
  replicasets: "ReplicaSet",
  statefulsets: "StatefulSet",
  daemonsets: "DaemonSet",
  services: "Service",
  ingresses: "Ingress",
  endpoints: "Endpoints",
  configmaps: "ConfigMap",
  secrets: "Secret",
  serviceaccounts: "ServiceAccount",
  resourcequotas: "ResourceQuota",
  limitranges: "LimitRange",
  namespaces: "Namespace",
  nodes: "Node",
  persistentvolumes: "PersistentVolume",
  pvs: "PersistentVolume",
  pvcs: "PersistentVolumeClaim",
  storageclasses: "StorageClass",
  jobs: "Job",
  cronjobs: "CronJob",
  networkpolicies: "NetworkPolicy",
  horizontalpodautoscalers: "HorizontalPodAutoscaler",
  hpas: "HorizontalPodAutoscaler",
  poddisruptionbudgets: "PodDisruptionBudget",
  pdbs: "PodDisruptionBudget",
  endpointslices: "EndpointSlice",
  roles: "Role",
  clusterroles: "ClusterRole",
  rolebindings: "RoleBinding",
  clusterrolebindings: "ClusterRoleBinding",
  validatingwebhookconfigurations: "ValidatingWebhookConfiguration",
  mutatingwebhookconfigurations: "MutatingWebhookConfiguration",
  ciliumnetworkpolicies: "CiliumNetworkPolicy",
};

/**
 * Maps API kind to the URL path prefix for detail pages.
 * Must match the filesystem route structure under routes/.
 */
export const RESOURCE_DETAIL_PATHS: Record<string, string> = {
  pods: "/workloads/pods",
  deployments: "/workloads/deployments",
  replicasets: "/workloads/replicasets",
  statefulsets: "/workloads/statefulsets",
  daemonsets: "/workloads/daemonsets",
  jobs: "/workloads/jobs",
  cronjobs: "/workloads/cronjobs",
  services: "/networking/services",
  ingresses: "/networking/ingresses",
  endpoints: "/networking/endpoints",
  networkpolicies: "/networking/networkpolicies",
  persistentvolumes: "/cluster/pvs",
  pvs: "/cluster/pvs",
  pvcs: "/storage/pvcs",
  storageclasses: "/cluster/storageclasses",
  configmaps: "/config/configmaps",
  secrets: "/config/secrets",
  serviceaccounts: "/config/serviceaccounts",
  resourcequotas: "/config/resourcequotas",
  limitranges: "/config/limitranges",
  horizontalpodautoscalers: "/scaling/hpas",
  hpas: "/scaling/hpas",
  poddisruptionbudgets: "/scaling/pdbs",
  pdbs: "/scaling/pdbs",
  endpointslices: "/networking/endpointslices",
  roles: "/rbac/roles",
  clusterroles: "/rbac/clusterroles",
  rolebindings: "/rbac/rolebindings",
  clusterrolebindings: "/rbac/clusterrolebindings",
  validatingwebhookconfigurations: "/admin/validatingwebhooks",
  mutatingwebhookconfigurations: "/admin/mutatingwebhooks",
  ciliumnetworkpolicies: "/networking/cilium-policies",
  nodes: "/cluster/nodes",
  namespaces: "/cluster/namespaces",
};

/** Cluster-scoped resource kinds (no namespace in URL). */
export const CLUSTER_SCOPED_KINDS = new Set([
  "nodes",
  "namespaces",
  "clusterroles",
  "clusterrolebindings",
  "persistentvolumes",
  "pvs",
  "storageclasses",
  "validatingwebhookconfigurations",
  "mutatingwebhookconfigurations",
]);

/** Resource navigation sections for the sidebar. */
export const NAV_SECTIONS = [
  {
    title: "Cluster",
    items: [
      { label: "Overview", href: "/", icon: "dashboard" },
      { label: "Nodes", href: "/cluster/nodes", icon: "nodes" },
      { label: "Namespaces", href: "/cluster/namespaces", icon: "namespaces" },
      { label: "Events", href: "/cluster/events", icon: "events" },
      {
        label: "PersistentVolumes",
        href: "/cluster/pvs",
        icon: "pvcs",
      },
      {
        label: "StorageClasses",
        href: "/cluster/storageclasses",
        icon: "storage",
      },
    ],
  },
  {
    title: "Workloads",
    items: [
      {
        label: "Deployments",
        href: "/workloads/deployments",
        icon: "deployments",
      },
      {
        label: "StatefulSets",
        href: "/workloads/statefulsets",
        icon: "statefulsets",
      },
      {
        label: "DaemonSets",
        href: "/workloads/daemonsets",
        icon: "daemonsets",
      },
      { label: "Pods", href: "/workloads/pods", icon: "pods" },
      { label: "Jobs", href: "/workloads/jobs", icon: "jobs" },
      { label: "CronJobs", href: "/workloads/cronjobs", icon: "cronjobs" },
      {
        label: "ReplicaSets",
        href: "/workloads/replicasets",
        icon: "deployments",
      },
    ],
  },
  {
    title: "Networking",
    items: [
      { label: "Services", href: "/networking/services", icon: "services" },
      { label: "Ingresses", href: "/networking/ingresses", icon: "ingresses" },
      {
        label: "Network Policies",
        href: "/networking/networkpolicies",
        icon: "networkpolicies",
      },
      {
        label: "Cilium Policies",
        href: "/networking/cilium-policies",
        icon: "networkpolicies",
      },
      { label: "CNI Plugin", href: "/networking/cni", icon: "networking" },
      { label: "Endpoints", href: "/networking/endpoints", icon: "services" },
      {
        label: "EndpointSlices",
        href: "/networking/endpointslices",
        icon: "services",
      },
    ],
  },
  {
    title: "Storage",
    items: [
      {
        label: "Overview",
        href: "/storage/overview",
        icon: "storage",
      },
      {
        label: "Persistent Volume Claims",
        href: "/storage/pvcs",
        icon: "pvcs",
      },
      {
        label: "Snapshots",
        href: "/storage/snapshots",
        icon: "snapshots",
      },
    ],
  },
  {
    title: "Config",
    items: [
      { label: "ConfigMaps", href: "/config/configmaps", icon: "configmaps" },
      { label: "Secrets", href: "/config/secrets", icon: "secrets" },
      {
        label: "ServiceAccounts",
        href: "/config/serviceaccounts",
        icon: "roles",
      },
      {
        label: "ResourceQuotas",
        href: "/config/resourcequotas",
        icon: "configmaps",
      },
      {
        label: "LimitRanges",
        href: "/config/limitranges",
        icon: "configmaps",
      },
    ],
  },
  {
    title: "Scaling",
    items: [
      {
        label: "HorizontalPodAutoscalers",
        href: "/scaling/hpas",
        icon: "deployments",
      },
      {
        label: "PodDisruptionBudgets",
        href: "/scaling/pdbs",
        icon: "pods",
      },
    ],
  },
  {
    title: "Access Control",
    items: [
      { label: "Roles", href: "/rbac/roles", icon: "roles" },
      {
        label: "ClusterRoles",
        href: "/rbac/clusterroles",
        icon: "clusterroles",
      },
      {
        label: "RoleBindings",
        href: "/rbac/rolebindings",
        icon: "rolebindings",
      },
      {
        label: "ClusterRoleBindings",
        href: "/rbac/clusterrolebindings",
        icon: "clusterrolebindings",
      },
    ],
  },
  {
    title: "Monitoring",
    items: [
      { label: "Overview", href: "/monitoring", icon: "monitoring" },
      {
        label: "Dashboards",
        href: "/monitoring/dashboards",
        icon: "dashboards",
      },
      {
        label: "Prometheus",
        href: "/monitoring/prometheus",
        icon: "prometheus",
      },
    ],
  },
  {
    title: "Alerting",
    items: [
      { label: "Active Alerts", href: "/alerting", icon: "alerts" },
      { label: "Alert Rules", href: "/alerting/rules", icon: "rules" },
      { label: "Settings", href: "/alerting/settings", icon: "settings" },
    ],
  },
  {
    title: "Tools",
    items: [
      { label: "YAML Apply", href: "/tools/yaml-apply", icon: "yaml" },
      {
        label: "StorageClass Wizard",
        href: "/tools/storageclass-wizard",
        icon: "storage",
      },
    ],
  },
  {
    title: "Admin",
    items: [
      {
        label: "ValidatingWebhooks",
        href: "/admin/validatingwebhooks",
        icon: "rules",
      },
      {
        label: "MutatingWebhooks",
        href: "/admin/mutatingwebhooks",
        icon: "rules",
      },
    ],
  },
  {
    title: "Settings",
    items: [
      {
        label: "Clusters",
        href: "/settings/clusters",
        icon: "nodes",
      },
      {
        label: "Authentication",
        href: "/settings/auth",
        icon: "settings",
      },
      {
        label: "Audit Log",
        href: "/settings/audit",
        icon: "settings",
      },
    ],
  },
] as const;
