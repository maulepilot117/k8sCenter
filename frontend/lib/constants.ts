/** Backend API base URL. In dev, the Fresh BFF proxy forwards to this. */
export const BACKEND_URL = typeof Deno !== "undefined"
  ? Deno.env.get("BACKEND_URL") ?? "http://localhost:8080"
  : "http://localhost:8080";

/** Cluster ID header — always "local" in Phase 1 (single cluster). */
export const CLUSTER_ID = "local";

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
    ],
  },
  {
    title: "Storage",
    items: [
      {
        label: "Persistent Volume Claims",
        href: "/storage/pvcs",
        icon: "pvcs",
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
] as const;
