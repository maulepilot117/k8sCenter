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

/** @deprecated Use DOMAIN_SECTIONS instead */
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
      {
        label: "Network Flows",
        href: "/networking/flows",
        icon: "networking",
      },
      { label: "Overview", href: "/networking", icon: "networking" },
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
      {
        label: "RBAC Overview",
        href: "/rbac/overview",
        icon: "roles",
      },
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
        label: "General",
        href: "/settings/general",
        icon: "settings",
      },
      {
        label: "Clusters",
        href: "/settings/clusters",
        icon: "nodes",
      },
      {
        label: "Users",
        href: "/settings/users",
        icon: "settings",
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

/** Domain-oriented navigation for the redesigned icon rail. */
export interface DomainSection {
  id: string;
  label: string;
  icon: string;
  href: string;
  tabs?: { label: string; href: string; kind?: string; count?: boolean }[];
}

export const DOMAIN_SECTIONS: DomainSection[] = [
  {
    id: "overview",
    label: "Overview",
    icon: "grid",
    href: "/",
  },
  {
    id: "workloads",
    label: "Workloads",
    icon: "box",
    href: "/workloads",
    tabs: [
      {
        label: "Deployments",
        href: "/workloads/deployments",
        kind: "deployments",
        count: true,
      },
      {
        label: "StatefulSets",
        href: "/workloads/statefulsets",
        kind: "statefulsets",
        count: true,
      },
      {
        label: "DaemonSets",
        href: "/workloads/daemonsets",
        kind: "daemonsets",
        count: true,
      },
      { label: "Pods", href: "/workloads/pods", kind: "pods", count: true },
      { label: "Jobs", href: "/workloads/jobs", kind: "jobs", count: true },
      {
        label: "CronJobs",
        href: "/workloads/cronjobs",
        kind: "cronjobs",
        count: true,
      },
      {
        label: "ReplicaSets",
        href: "/workloads/replicasets",
        kind: "replicasets",
        count: true,
      },
    ],
  },
  {
    id: "network",
    label: "Network",
    icon: "globe",
    href: "/networking",
    tabs: [
      { label: "Overview", href: "/networking" },
      {
        label: "Services",
        href: "/networking/services",
        kind: "services",
        count: true,
      },
      {
        label: "Ingresses",
        href: "/networking/ingresses",
        kind: "ingresses",
        count: true,
      },
      {
        label: "Network Policies",
        href: "/networking/networkpolicies",
        kind: "networkpolicies",
        count: true,
      },
      {
        label: "Cilium Policies",
        href: "/networking/cilium-policies",
        kind: "ciliumnetworkpolicies",
        count: true,
      },
      { label: "Flows", href: "/networking/flows" },
      {
        label: "Endpoints",
        href: "/networking/endpoints",
        kind: "endpoints",
        count: true,
      },
      {
        label: "EndpointSlices",
        href: "/networking/endpointslices",
        kind: "endpointslices",
        count: true,
      },
      { label: "Gateway API", href: "/networking/gateway-api" },
    ],
  },
  {
    id: "storage",
    label: "Storage",
    icon: "harddrive",
    href: "/storage",
    tabs: [
      { label: "Overview", href: "/storage/overview" },
      {
        label: "PVCs",
        href: "/storage/pvcs",
        kind: "persistentvolumeclaims",
        count: true,
      },
      {
        label: "PVs",
        href: "/cluster/pvs",
        kind: "persistentvolumes",
        count: true,
      },
      {
        label: "Storage Classes",
        href: "/cluster/storageclasses",
        kind: "storageclasses",
        count: true,
      },
      { label: "Snapshots", href: "/storage/snapshots" },
    ],
  },
  {
    id: "config",
    label: "Config",
    icon: "sliders",
    href: "/config",
    tabs: [
      {
        label: "ConfigMaps",
        href: "/config/configmaps",
        kind: "configmaps",
        count: true,
      },
      {
        label: "Secrets",
        href: "/config/secrets",
        kind: "secrets",
        count: true,
      },
      {
        label: "Service Accounts",
        href: "/config/serviceaccounts",
        kind: "serviceaccounts",
        count: true,
      },
      {
        label: "Resource Quotas",
        href: "/config/resourcequotas",
        kind: "resourcequotas",
        count: true,
      },
      {
        label: "Limit Ranges",
        href: "/config/limitranges",
        kind: "limitranges",
        count: true,
      },
      {
        label: "Namespace Limits",
        href: "/config/namespace-limits",
      },
    ],
  },
  {
    id: "security",
    label: "Security",
    icon: "shield",
    href: "/rbac",
    tabs: [
      { label: "Overview", href: "/rbac/overview" },
      { label: "Roles", href: "/rbac/roles", kind: "roles", count: true },
      {
        label: "Cluster Roles",
        href: "/rbac/clusterroles",
        kind: "clusterroles",
        count: true,
      },
      {
        label: "Role Bindings",
        href: "/rbac/rolebindings",
        kind: "rolebindings",
        count: true,
      },
      {
        label: "Cluster Role Bindings",
        href: "/rbac/clusterrolebindings",
        kind: "clusterrolebindings",
        count: true,
      },
      { label: "Webhooks", href: "/admin/validatingwebhooks" },
      { label: "Policies", href: "/security/policies" },
      { label: "Violations", href: "/security/violations" },
      { label: "Compliance", href: "/security/compliance" },
      { label: "Vulnerabilities", href: "/security/vulnerabilities" },
      { label: "Certificates", href: "/security/certificates" },
    ],
  },
  {
    id: "observability",
    label: "Observability",
    icon: "activity",
    href: "/monitoring",
    tabs: [
      { label: "Overview", href: "/monitoring" },
      { label: "Log Explorer", href: "/observability/logs" },
      { label: "Topology", href: "/observability/topology" },
      { label: "Investigate", href: "/observability/investigate" },
      { label: "Dashboards", href: "/monitoring/dashboards" },
      { label: "Prometheus", href: "/monitoring/prometheus" },
      { label: "Alerts", href: "/alerting" },
      { label: "Alert Rules", href: "/alerting/rules" },
    ],
  },
  {
    id: "gitops",
    label: "GitOps",
    icon: "git-branch",
    href: "/gitops",
    tabs: [
      { label: "Applications", href: "/gitops/applications" },
      { label: "ApplicationSets", href: "/gitops/applicationsets" },
      { label: "Notifications", href: "/gitops/notifications" },
    ],
  },
  {
    id: "backup",
    label: "Backup",
    icon: "archive",
    href: "/backup",
    tabs: [
      { label: "Backups", href: "/backup/backups" },
      { label: "Restores", href: "/backup/restores" },
      { label: "Schedules", href: "/backup/schedules" },
    ],
  },
  {
    id: "tools",
    label: "Tools",
    icon: "wrench",
    href: "/tools",
    tabs: [
      { label: "YAML Apply", href: "/tools/yaml-apply" },
      { label: "StorageClass Wizard", href: "/tools/storageclass-wizard" },
    ],
  },
  {
    id: "extensions",
    label: "Extensions",
    icon: "puzzle",
    href: "/extensions",
  },
];

export const SETTINGS_SECTION: DomainSection = {
  id: "settings",
  label: "Settings",
  icon: "settings",
  href: "/settings/general",
  tabs: [
    { label: "General", href: "/settings/general" },
    { label: "Clusters", href: "/settings/clusters" },
    { label: "Users", href: "/settings/users" },
    { label: "Authentication", href: "/settings/auth" },
    { label: "Audit Log", href: "/settings/audit" },
  ],
};
