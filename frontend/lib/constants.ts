/**
 * Reads BACKEND_URL from the runtime's env API.
 *
 * This carried a `Deno.env.get` branch ahead of the `process.env` one while
 * both trees existed. U12 removed it with the Fresh tree: Bun is the only
 * runtime that loads this module now, and a dead branch reading a global
 * nothing defines is worse than no branch at all -- it reads as support for
 * a runtime that is no longer tested.
 *
 * `process` is still reached through `globalThis` with a local cast rather
 * than the bare identifier. This module is imported by islands as well as by
 * the server, and the browser bundle has no `process`; the optional-chained
 * read returns undefined there instead of throwing, which is what lets the
 * hardcoded default below stand in.
 *
 * The value matters: the Helm chart sets BACKEND_URL on the frontend
 * Deployment (R13), and it is the one environment variable that pod is given
 * at all (R17).
 */
export function readBackendUrlFromEnv(): string | undefined {
  const g = globalThis as unknown as {
    process?: { env?: Record<string, string | undefined> };
  };
  return g.process?.env?.BACKEND_URL;
}

/**
 * Backend API base URL. In dev, the Astro dev server's plugin proxies to
 * this; in production, frontend/server/prod.ts does.
 */
export const BACKEND_URL = readBackendUrlFromEnv() ?? "http://localhost:8080";

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

/**
 * Kinds the preferences API will accept in a pin.
 *
 * Mirrors the adapter registry in backend/internal/k8s/resources: ValidatePin
 * resolves `resourceKind` through resources.GetAdapter, so a kind absent here
 * is rejected with `unknown_resource_kind` no matter what the UI does. Offering
 * a pin control for such a kind produces a button that can only ever fail --
 * ciliumnetworkpolicies is the one detail page in that position today, because
 * it is served by its own handler rather than by a registered adapter.
 *
 * Keep this in lockstep with the adapters' Kind() returns. A new detail page
 * for an unregistered kind must NOT be added here.
 */
export const PINNABLE_KINDS = new Set([
  "clusterrolebindings",
  "clusterroles",
  "configmaps",
  "cronjobs",
  "daemonsets",
  "deployments",
  "endpoints",
  "endpointslices",
  "events",
  "hpas",
  "ingresses",
  "jobs",
  "limitranges",
  "mutatingwebhookconfigurations",
  "namespaces",
  "networkpolicies",
  "nodes",
  "pdbs",
  "pods",
  "pvcs",
  "pvs",
  "replicasets",
  "resourcequotas",
  "rolebindings",
  "roles",
  "secrets",
  "serviceaccounts",
  "services",
  "statefulsets",
  "storageclasses",
  "validatingwebhookconfigurations",
]);

/**
 * PascalCase Kubernetes kinds the diagnostics backend can investigate.
 *
 * Mirrors kindToResource in backend/internal/diagnostics/handler.go, which
 * answers 400 "unsupported resource kind" for anything else. The Investigate
 * link interpolates RESOURCE_API_KINDS[kind], so this set is keyed the same
 * way. Widening it requires widening kindToResource first.
 */
export const INVESTIGATE_KINDS = new Set([
  "Deployment",
  "StatefulSet",
  "DaemonSet",
  "Pod",
  "Service",
  "PersistentVolumeClaim",
]);

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

// ============================================================================
// Grouped, domain-oriented navigation for the two-pane shell.
//
// `groups` replaces the old flat `tabs` array. Each domain section holds one
// or more named groups, each with a list of NavItems. The SecondaryNav island
// (built in a later task) will render these vertically.
// `health` drives the colored dot ("ok" | "warn" | "crit").
// `kind` + `count` let SecondaryNav show a live resource count badge.
// ============================================================================

export type Health = "ok" | "warn" | "crit";

export interface NavItem {
  label: string;
  href: string;
  kind?: string; // k8s plural kind for live count, e.g. "deployments"
  count?: boolean; // show count badge
  health?: Health;
}

export interface NavGroup {
  header: string;
  items: NavItem[];
}

export interface DomainSection {
  id: string;
  label: string;
  icon: string; // key into IconRail ICONS map
  href: string; // landing route when the rail icon is clicked
  alert?: Health; // rail badge dot
  groups?: NavGroup[];
}

/**
 * Flatten all groups[].items into a single ordered array.
 * Used by CommandPalette, WorkloadsDashboard, and other flat-list consumers.
 */
export function flattenGroups(section: DomainSection): NavItem[] {
  if (!section.groups) return [];
  return section.groups.flatMap((g) => g.items);
}

export const DOMAIN_SECTIONS: DomainSection[] = [
  { id: "overview", label: "Overview", icon: "grid", href: "/" },

  {
    id: "workloads",
    label: "Workloads",
    icon: "box",
    href: "/workloads/deployments",
    groups: [
      {
        header: "Controllers",
        items: [
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
          {
            label: "ReplicaSets",
            href: "/workloads/replicasets",
            kind: "replicasets",
            count: true,
          },
        ],
      },
      {
        header: "Pods & Jobs",
        items: [
          { label: "Pods", href: "/workloads/pods", kind: "pods", count: true },
          { label: "Jobs", href: "/workloads/jobs", kind: "jobs", count: true },
          {
            label: "CronJobs",
            href: "/workloads/cronjobs",
            kind: "cronjobs",
            count: true,
          },
        ],
      },
    ],
  },

  {
    id: "network",
    label: "Network",
    icon: "globe",
    href: "/networking",
    groups: [
      {
        header: "Connectivity",
        items: [
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
        ],
      },
      {
        header: "Policies",
        items: [
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
        ],
      },
      {
        header: "Service Mesh",
        items: [
          { label: "Overview", href: "/networking/mesh" },
          { label: "Traffic Routing", href: "/networking/mesh/routing" },
          { label: "mTLS Posture", href: "/networking/mesh/mtls" },
          { label: "Gateway API", href: "/networking/gateway-api" },
          { label: "Live Flows", href: "/networking/flows" },
        ],
      },
    ],
  },

  {
    id: "storage",
    label: "Storage",
    icon: "harddrive",
    href: "/storage/overview",
    groups: [
      {
        header: "Volumes",
        items: [
          {
            label: "Persistent Volume Claims",
            href: "/storage/pvcs",
            kind: "persistentvolumeclaims",
            count: true,
          },
          {
            label: "Persistent Volumes",
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
    ],
  },

  {
    id: "config",
    label: "Config",
    icon: "sliders",
    href: "/config/configmaps",
    groups: [
      {
        header: "Application",
        items: [
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
        ],
      },
      {
        header: "Governance",
        items: [
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
          { label: "Namespace Limits", href: "/config/namespace-limits" },
        ],
      },
    ],
  },

  {
    id: "security",
    label: "Security",
    icon: "shield",
    href: "/rbac/overview",
    groups: [
      {
        header: "Access Control",
        items: [
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
        ],
      },
      {
        header: "Posture",
        items: [
          { label: "Policies", href: "/security/policies" },
          { label: "Violations", href: "/security/violations" },
          { label: "Compliance", href: "/security/compliance" },
          { label: "Vulnerabilities", href: "/security/vulnerabilities" },
          { label: "Certificates", href: "/security/certificates" },
        ],
      },
    ],
  },

  {
    id: "observability",
    label: "Observability",
    icon: "activity",
    href: "/monitoring",
    groups: [
      {
        header: "Explore",
        items: [
          { label: "Service Topology", href: "/observability/topology" },
          { label: "Log Explorer", href: "/observability/logs" },
          { label: "Investigate", href: "/observability/investigate" },
        ],
      },
      {
        header: "Metrics",
        items: [
          { label: "Overview", href: "/monitoring" },
          { label: "Dashboards", href: "/monitoring/dashboards" },
          { label: "Prometheus", href: "/monitoring/prometheus" },
        ],
      },
      {
        header: "Alerts",
        items: [
          { label: "Active Alerts", href: "/alerting" },
          { label: "Alert Rules", href: "/alerting/rules" },
        ],
      },
    ],
  },

  {
    id: "gitops",
    label: "GitOps",
    icon: "git-branch",
    href: "/gitops/applications",
    groups: [
      {
        header: "Delivery",
        items: [
          { label: "Applications", href: "/gitops/applications" },
          { label: "ApplicationSets", href: "/gitops/applicationsets" },
          { label: "Notifications", href: "/gitops/notifications" },
        ],
      },
    ],
  },

  {
    id: "external-secrets",
    label: "External Secrets",
    icon: "key",
    href: "/external-secrets/dashboard",
    groups: [
      {
        header: "External Secrets",
        items: [
          { label: "Dashboard", href: "/external-secrets/dashboard" },
          {
            label: "ExternalSecrets",
            href: "/external-secrets/external-secrets",
          },
          {
            label: "ClusterExternalSecrets",
            href: "/external-secrets/cluster-external-secrets",
          },
          { label: "Secret Stores", href: "/external-secrets/stores" },
          { label: "Cluster Stores", href: "/external-secrets/cluster-stores" },
          { label: "PushSecrets", href: "/external-secrets/push-secrets" },
          { label: "Provider Chain", href: "/external-secrets/chain" },
        ],
      },
    ],
  },

  {
    id: "backup",
    label: "Backup",
    icon: "archive",
    href: "/backup/backups",
    groups: [
      {
        header: "Protection",
        items: [
          { label: "Backups", href: "/backup/backups" },
          { label: "Restores", href: "/backup/restores" },
          { label: "Schedules", href: "/backup/schedules" },
        ],
      },
    ],
  },

  {
    id: "tools",
    label: "Tools",
    icon: "wrench",
    href: "/tools/yaml-apply",
    groups: [
      {
        header: "Tools",
        items: [
          { label: "YAML Apply", href: "/tools/yaml-apply" },
          { label: "StorageClass Wizard", href: "/tools/storageclass-wizard" },
        ],
      },
    ],
  },
];

export const SETTINGS_SECTION: DomainSection = {
  id: "settings",
  label: "Settings",
  icon: "settings",
  href: "/settings/general",
  groups: [
    {
      header: "Settings",
      items: [
        { label: "General", href: "/settings/general" },
        { label: "Clusters", href: "/settings/clusters" },
        { label: "Users", href: "/settings/users" },
        { label: "Authentication", href: "/settings/auth" },
        { label: "Audit Log", href: "/settings/audit" },
      ],
    },
  ],
};

const _ALL_SECTIONS = [...DOMAIN_SECTIONS, SETTINGS_SECTION];

/** Which top-level domain owns this path. */
export function getActiveDomain(path: string): string | null {
  // Explicit overrides for routes that don't share a domain's prefix.
  if (path.startsWith("/cluster")) return "overview";
  if (path.startsWith("/rbac")) return "security";
  if (path.startsWith("/admin")) return "security";
  if (path.startsWith("/settings")) return "settings";
  if (path.startsWith("/scaling")) return "workloads";

  for (const s of _ALL_SECTIONS) {
    if (s.href === "/" && path === "/") return s.id;
    if (s.href !== "/" && path.startsWith(`/${s.id}`)) return s.id;
    if (
      s.groups?.some((g) =>
        g.items.some(
          (it) => path === it.href || path.startsWith(`${it.href}/`),
        ),
      )
    ) {
      return s.id;
    }
  }
  return null;
}

export function domainById(id: string | null): DomainSection | undefined {
  if (!id) return undefined;
  return _ALL_SECTIONS.find((s) => s.id === id);
}
