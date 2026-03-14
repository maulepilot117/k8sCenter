/** Backend API base URL. In dev, the Fresh BFF proxy forwards to this. */
export const BACKEND_URL = typeof Deno !== "undefined"
  ? Deno.env.get("BACKEND_URL") ?? "http://localhost:8080"
  : "http://localhost:8080";

/** Cluster ID header — always "local" in Phase 1 (single cluster). */
export const CLUSTER_ID = "local";

/**
 * Maps lowercase plural API kind to PascalCase Kubernetes API kind.
 * Used for event filtering (involvedObject.kind uses PascalCase).
 */
export const RESOURCE_API_KINDS: Record<string, string> = {
  pods: "Pod",
  deployments: "Deployment",
  statefulsets: "StatefulSet",
  daemonsets: "DaemonSet",
  services: "Service",
  ingresses: "Ingress",
  configmaps: "ConfigMap",
  secrets: "Secret",
  namespaces: "Namespace",
  nodes: "Node",
  pvcs: "PersistentVolumeClaim",
  jobs: "Job",
  cronjobs: "CronJob",
  networkpolicies: "NetworkPolicy",
  roles: "Role",
  clusterroles: "ClusterRole",
  rolebindings: "RoleBinding",
  clusterrolebindings: "ClusterRoleBinding",
};

/**
 * Maps API kind to the URL path prefix for detail pages.
 * Must match the filesystem route structure under routes/.
 */
export const RESOURCE_DETAIL_PATHS: Record<string, string> = {
  pods: "/workloads/pods",
  deployments: "/workloads/deployments",
  statefulsets: "/workloads/statefulsets",
  daemonsets: "/workloads/daemonsets",
  jobs: "/workloads/jobs",
  cronjobs: "/workloads/cronjobs",
  services: "/networking/services",
  ingresses: "/networking/ingresses",
  networkpolicies: "/networking/networkpolicies",
  pvcs: "/storage/pvcs",
  configmaps: "/config/configmaps",
  secrets: "/config/secrets",
  roles: "/rbac/roles",
  clusterroles: "/rbac/clusterroles",
  rolebindings: "/rbac/rolebindings",
  clusterrolebindings: "/rbac/clusterrolebindings",
  nodes: "/cluster/nodes",
  namespaces: "/cluster/namespaces",
};

/** Cluster-scoped resource kinds (no namespace in URL). */
export const CLUSTER_SCOPED_KINDS = new Set([
  "nodes",
  "namespaces",
  "clusterroles",
  "clusterrolebindings",
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
      { label: "CNI Plugin", href: "/networking/cni", icon: "networking" },
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
] as const;
