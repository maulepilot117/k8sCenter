/**
 * Column definitions for each resource type used by ResourceTable.
 * Each column config maps to a DataTable Column<K8sResource>.
 */
import type { ComponentChildren } from "preact";
import { h } from "preact";
import type {
  ClusterRole,
  ClusterRoleBinding,
  ConfigMap,
  CronJob,
  DaemonSet,
  Deployment,
  Endpoints,
  EndpointSlice,
  HorizontalPodAutoscaler,
  Ingress,
  Job,
  K8sEvent,
  K8sResource,
  LimitRange,
  Namespace,
  NetworkPolicy,
  Node,
  PersistentVolume,
  PersistentVolumeClaim,
  Pod,
  PodDisruptionBudget,
  ReplicaSet,
  ResourceQuota,
  Role,
  RoleBinding,
  Secret,
  Service,
  ServiceAccount,
  StatefulSet,
  StorageClass,
} from "@/lib/k8s-types.ts";
import type { Column } from "@/components/ui/DataTable.tsx";
import { statusColor } from "@/lib/status-colors.ts";
import { age } from "@/lib/format.ts";

// Helper to create a StatusBadge lazily (avoids importing island in server context)
function badge(text: string): ComponentChildren {
  return h("span", {
    class:
      `inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium ring-1 ring-inset ${
        statusColor(text)
      }`,
  }, text);
}

// Shared columns
const nameCol: Column<K8sResource> = {
  key: "name",
  label: "Name",
  sortable: true,
  render: (r) => r.metadata.name,
};
const namespaceCol: Column<K8sResource> = {
  key: "namespace",
  label: "Namespace",
  sortable: true,
  render: (r) => r.metadata.namespace ?? "-",
};
const ageCol: Column<K8sResource> = {
  key: "age",
  label: "Age",
  sortable: true,
  render: (r) => age(r.metadata.creationTimestamp),
};

// ---- Per-resource column sets ----

const podColumns: Column<K8sResource>[] = [
  nameCol,
  namespaceCol,
  {
    key: "status",
    label: "Status",
    sortable: true,
    render: (r) => badge((r as Pod).status?.phase ?? "Unknown"),
  },
  {
    key: "ready",
    label: "Ready",
    render: (r) => {
      const p = r as Pod;
      const containers = p.status?.containerStatuses ?? [];
      const readyCount = containers.filter((c) => c.ready).length;
      return `${readyCount}/${
        containers.length || p.spec?.containers?.length || 0
      }`;
    },
  },
  {
    key: "restarts",
    label: "Restarts",
    sortable: true,
    render: (r) => {
      const containers = (r as Pod).status?.containerStatuses ?? [];
      return String(containers.reduce((sum, c) => sum + c.restartCount, 0));
    },
  },
  {
    key: "node",
    label: "Node",
    render: (r) => (r as Pod).spec?.nodeName ?? "-",
  },
  ageCol,
];

const deploymentColumns: Column<K8sResource>[] = [
  nameCol,
  namespaceCol,
  {
    key: "ready",
    label: "Ready",
    render: (r) => {
      const d = r as Deployment;
      return `${d.status?.readyReplicas ?? 0}/${d.spec?.replicas ?? 0}`;
    },
  },
  {
    key: "upToDate",
    label: "Up-to-date",
    render: (r) => String((r as Deployment).status?.updatedReplicas ?? 0),
  },
  {
    key: "available",
    label: "Available",
    render: (r) => String((r as Deployment).status?.availableReplicas ?? 0),
  },
  ageCol,
];

const statefulsetColumns: Column<K8sResource>[] = [
  nameCol,
  namespaceCol,
  {
    key: "ready",
    label: "Ready",
    render: (r) => {
      const s = r as StatefulSet;
      return `${s.status?.readyReplicas ?? 0}/${s.spec?.replicas ?? 0}`;
    },
  },
  ageCol,
];

const daemonsetColumns: Column<K8sResource>[] = [
  nameCol,
  namespaceCol,
  {
    key: "desired",
    label: "Desired",
    render: (r) => String((r as DaemonSet).status?.desiredNumberScheduled ?? 0),
  },
  {
    key: "ready",
    label: "Ready",
    render: (r) => String((r as DaemonSet).status?.numberReady ?? 0),
  },
  {
    key: "available",
    label: "Available",
    render: (r) => String((r as DaemonSet).status?.numberAvailable ?? 0),
  },
  ageCol,
];

const serviceColumns: Column<K8sResource>[] = [
  nameCol,
  namespaceCol,
  {
    key: "type",
    label: "Type",
    sortable: true,
    render: (r) => (r as Service).spec?.type ?? "-",
  },
  {
    key: "clusterIP",
    label: "Cluster IP",
    render: (r) => (r as Service).spec?.clusterIP ?? "-",
  },
  {
    key: "ports",
    label: "Ports",
    render: (r) => {
      const ports = (r as Service).spec?.ports;
      if (!ports?.length) return "-";
      return ports.map((p) => `${p.port}/${p.protocol ?? "TCP"}`).join(", ");
    },
  },
  ageCol,
];

const ingressColumns: Column<K8sResource>[] = [
  nameCol,
  namespaceCol,
  {
    key: "hosts",
    label: "Hosts",
    render: (r) => {
      const rules = (r as Ingress).spec?.rules;
      if (!rules?.length) return "-";
      return rules.map((rule) => rule.host ?? "*").join(", ");
    },
  },
  {
    key: "address",
    label: "Address",
    render: (r) => {
      const lb = (r as Ingress).status?.loadBalancer?.ingress;
      if (!lb?.length) return "-";
      return lb.map((i) => i.ip ?? i.hostname ?? "").join(", ");
    },
  },
  ageCol,
];

const configmapColumns: Column<K8sResource>[] = [
  nameCol,
  namespaceCol,
  {
    key: "keys",
    label: "Keys",
    render: (r) => {
      const data = (r as ConfigMap).data;
      return String(data ? Object.keys(data).length : 0);
    },
  },
  ageCol,
];

const secretColumns: Column<K8sResource>[] = [
  nameCol,
  namespaceCol,
  {
    key: "type",
    label: "Type",
    sortable: true,
    render: (r) => (r as Secret).type ?? "Opaque",
  },
  {
    key: "keys",
    label: "Keys",
    render: (r) => {
      const data = (r as Secret).data;
      return String(data ? Object.keys(data).length : 0);
    },
  },
  ageCol,
];

const namespaceColumns: Column<K8sResource>[] = [
  nameCol,
  {
    key: "status",
    label: "Status",
    sortable: true,
    render: (r) => badge((r as Namespace).status?.phase ?? "Active"),
  },
  ageCol,
];

const nodeColumns: Column<K8sResource>[] = [
  nameCol,
  {
    key: "status",
    label: "Status",
    render: (r) => {
      const n = r as Node;
      const ready = n.status?.conditions?.find((c) => c.type === "Ready");
      return badge(ready?.status === "True" ? "Ready" : "Not Ready");
    },
  },
  {
    key: "roles",
    label: "Roles",
    render: (r) => {
      const labels = r.metadata.labels ?? {};
      const roles = Object.keys(labels)
        .filter((k) => k.startsWith("node-role.kubernetes.io/"))
        .map((k) => k.replace("node-role.kubernetes.io/", ""));
      return roles.length ? roles.join(", ") : "<none>";
    },
  },
  {
    key: "version",
    label: "Version",
    render: (r) => (r as Node).status?.nodeInfo?.kubeletVersion ?? "-",
  },
  ageCol,
];

const pvcColumns: Column<K8sResource>[] = [
  nameCol,
  namespaceCol,
  {
    key: "status",
    label: "Status",
    sortable: true,
    render: (r) =>
      badge((r as PersistentVolumeClaim).status?.phase ?? "Pending"),
  },
  {
    key: "capacity",
    label: "Capacity",
    render: (r) => {
      const cap = (r as PersistentVolumeClaim).status?.capacity;
      return cap?.storage ?? "-";
    },
  },
  {
    key: "storageClass",
    label: "Storage Class",
    render: (r) => (r as PersistentVolumeClaim).spec?.storageClassName ?? "-",
  },
  ageCol,
];

const jobColumns: Column<K8sResource>[] = [
  nameCol,
  namespaceCol,
  {
    key: "completions",
    label: "Completions",
    render: (r) => {
      const j = r as Job;
      return `${j.status?.succeeded ?? 0}/${j.spec?.completions ?? 1}`;
    },
  },
  {
    key: "status",
    label: "Status",
    render: (r) => {
      const j = r as Job;
      if (j.status?.completionTime) return badge("Complete");
      if ((j.status?.failed ?? 0) > 0) return badge("Failed");
      if ((j.status?.active ?? 0) > 0) return badge("Running");
      return badge("Pending");
    },
  },
  ageCol,
];

const cronjobColumns: Column<K8sResource>[] = [
  nameCol,
  namespaceCol,
  {
    key: "schedule",
    label: "Schedule",
    render: (r) => (r as CronJob).spec?.schedule ?? "-",
  },
  {
    key: "suspend",
    label: "Suspend",
    render: (r) => (r as CronJob).spec?.suspend ? "True" : "False",
  },
  {
    key: "lastSchedule",
    label: "Last Schedule",
    render: (r) => {
      const t = (r as CronJob).status?.lastScheduleTime;
      return t ? age(t) : "-";
    },
  },
  ageCol,
];

const networkpolicyColumns: Column<K8sResource>[] = [
  nameCol,
  namespaceCol,
  {
    key: "policyTypes",
    label: "Policy Types",
    render: (r) => (r as NetworkPolicy).spec?.policyTypes?.join(", ") ?? "-",
  },
  ageCol,
];

const roleColumns: Column<K8sResource>[] = [
  nameCol,
  namespaceCol,
  {
    key: "rules",
    label: "Rules",
    render: (r) => String((r as Role).rules?.length ?? 0),
  },
  ageCol,
];

const clusterroleColumns: Column<K8sResource>[] = [
  nameCol,
  {
    key: "rules",
    label: "Rules",
    render: (r) => String((r as ClusterRole).rules?.length ?? 0),
  },
  ageCol,
];

const rolebindingColumns: Column<K8sResource>[] = [
  nameCol,
  namespaceCol,
  {
    key: "roleRef",
    label: "Role",
    render: (r) => {
      const rb = r as RoleBinding;
      return `${rb.roleRef.kind}/${rb.roleRef.name}`;
    },
  },
  {
    key: "subjects",
    label: "Subjects",
    render: (r) => String((r as RoleBinding).subjects?.length ?? 0),
  },
  ageCol,
];

const clusterrolebindingColumns: Column<K8sResource>[] = [
  nameCol,
  {
    key: "roleRef",
    label: "Role",
    render: (r) => {
      const crb = r as ClusterRoleBinding;
      return `${crb.roleRef.kind}/${crb.roleRef.name}`;
    },
  },
  {
    key: "subjects",
    label: "Subjects",
    render: (r) => String((r as ClusterRoleBinding).subjects?.length ?? 0),
  },
  ageCol,
];

const eventColumns: Column<K8sResource>[] = [
  {
    key: "type",
    label: "Type",
    sortable: true,
    render: (r) => badge((r as K8sEvent).type ?? "Normal"),
  },
  {
    key: "reason",
    label: "Reason",
    sortable: true,
    render: (r) => (r as K8sEvent).reason ?? "-",
  },
  {
    key: "object",
    label: "Object",
    render: (r) => {
      const obj = (r as K8sEvent).involvedObject;
      if (!obj) return "-";
      return `${obj.kind}/${obj.name}`;
    },
  },
  {
    key: "message",
    label: "Message",
    render: (r) => (r as K8sEvent).message ?? "-",
    class: "max-w-md truncate",
  },
  {
    key: "count",
    label: "Count",
    render: (r) => String((r as K8sEvent).count ?? 1),
  },
  {
    key: "lastSeen",
    label: "Last Seen",
    sortable: true,
    render: (r) => {
      const t = (r as K8sEvent).lastTimestamp;
      return t ? age(t) : "-";
    },
  },
];

const validatingWebhookColumns: Column<K8sResource>[] = [
  nameCol,
  {
    key: "webhooks",
    label: "Webhooks",
    render: (r) => {
      const webhooks = (r as K8sResource & { webhooks?: unknown[] }).webhooks;
      return String(webhooks?.length ?? 0);
    },
  },
  ageCol,
];

const mutatingWebhookColumns: Column<K8sResource>[] = [
  nameCol,
  {
    key: "webhooks",
    label: "Webhooks",
    render: (r) => {
      const webhooks = (r as K8sResource & { webhooks?: unknown[] }).webhooks;
      return String(webhooks?.length ?? 0);
    },
  },
  ageCol,
];

const replicasetColumns: Column<K8sResource>[] = [
  nameCol,
  namespaceCol,
  {
    key: "desired",
    label: "Desired",
    render: (r) => String((r as ReplicaSet).spec?.replicas ?? 0),
  },
  {
    key: "current",
    label: "Current",
    render: (r) => String((r as ReplicaSet).status?.replicas ?? 0),
  },
  {
    key: "ready",
    label: "Ready",
    render: (r) => String((r as ReplicaSet).status?.readyReplicas ?? 0),
  },
  ageCol,
];

const endpointColumns: Column<K8sResource>[] = [
  nameCol,
  namespaceCol,
  {
    key: "addresses",
    label: "Addresses",
    render: (r) => {
      const ep = r as Endpoints;
      const count = ep.subsets?.reduce(
        (sum, s) => sum + (s.addresses?.length ?? 0),
        0,
      ) ?? 0;
      return String(count);
    },
  },
  ageCol,
];

const hpaColumns: Column<K8sResource>[] = [
  nameCol,
  namespaceCol,
  {
    key: "targets",
    label: "Targets",
    render: (r) => {
      const hpa = r as HorizontalPodAutoscaler;
      const metrics = hpa.spec?.metrics;
      if (!metrics?.length) return "-";
      return metrics.map((m) => {
        if (m.resource?.target?.averageUtilization) {
          const current = hpa.status?.currentMetrics?.find(
            (cm) => cm.resource?.name === m.resource?.name,
          );
          const currentVal = current?.resource?.current?.averageUtilization;
          return `${
            currentVal ?? "<unknown>"
          }%/${m.resource.target.averageUtilization}%`;
        }
        return m.type;
      }).join(", ");
    },
  },
  {
    key: "minReplicas",
    label: "Min",
    render: (r) =>
      String((r as HorizontalPodAutoscaler).spec?.minReplicas ?? 1),
  },
  {
    key: "maxReplicas",
    label: "Max",
    render: (r) =>
      String((r as HorizontalPodAutoscaler).spec?.maxReplicas ?? 0),
  },
  {
    key: "currentReplicas",
    label: "Replicas",
    render: (r) =>
      String((r as HorizontalPodAutoscaler).status?.currentReplicas ?? 0),
  },
  ageCol,
];

const pvColumns: Column<K8sResource>[] = [
  nameCol,
  {
    key: "capacity",
    label: "Capacity",
    render: (r) => (r as PersistentVolume).spec?.capacity?.storage ?? "-",
  },
  {
    key: "accessModes",
    label: "Access Modes",
    render: (r) => (r as PersistentVolume).spec?.accessModes?.join(", ") ?? "-",
  },
  {
    key: "reclaimPolicy",
    label: "Reclaim Policy",
    render: (r) =>
      (r as PersistentVolume).spec?.persistentVolumeReclaimPolicy ?? "-",
  },
  {
    key: "status",
    label: "Status",
    sortable: true,
    render: (r) => badge((r as PersistentVolume).status?.phase ?? "Available"),
  },
  {
    key: "storageClass",
    label: "Storage Class",
    render: (r) => (r as PersistentVolume).spec?.storageClassName ?? "-",
  },
  {
    key: "claim",
    label: "Claim",
    render: (r) => {
      const ref = (r as PersistentVolume).spec?.claimRef;
      return ref ? `${ref.namespace}/${ref.name}` : "-";
    },
  },
  ageCol,
];

const storageclassColumns: Column<K8sResource>[] = [
  nameCol,
  {
    key: "provisioner",
    label: "Provisioner",
    render: (r) => (r as StorageClass).provisioner ?? "-",
  },
  {
    key: "reclaimPolicy",
    label: "Reclaim Policy",
    render: (r) => (r as StorageClass).reclaimPolicy ?? "-",
  },
  {
    key: "volumeBindingMode",
    label: "Volume Binding Mode",
    render: (r) => (r as StorageClass).volumeBindingMode ?? "-",
  },
  ageCol,
];

const resourcequotaColumns: Column<K8sResource>[] = [
  nameCol,
  namespaceCol,
  ageCol,
];

const limitrangeColumns: Column<K8sResource>[] = [
  nameCol,
  namespaceCol,
  ageCol,
];

const serviceaccountColumns: Column<K8sResource>[] = [
  nameCol,
  namespaceCol,
  {
    key: "secrets",
    label: "Secrets",
    render: (r) => String((r as ServiceAccount).secrets?.length ?? 0),
  },
  ageCol,
];

const pdbColumns: Column<K8sResource>[] = [
  nameCol,
  namespaceCol,
  {
    key: "minAvailable",
    label: "Min Available",
    render: (r) => {
      const v = (r as PodDisruptionBudget).spec?.minAvailable;
      return v != null ? String(v) : "-";
    },
  },
  {
    key: "maxUnavailable",
    label: "Max Unavailable",
    render: (r) => {
      const v = (r as PodDisruptionBudget).spec?.maxUnavailable;
      return v != null ? String(v) : "-";
    },
  },
  {
    key: "currentHealthy",
    label: "Current Healthy",
    render: (r) =>
      String((r as PodDisruptionBudget).status?.currentHealthy ?? 0),
  },
  {
    key: "desiredHealthy",
    label: "Desired Healthy",
    render: (r) =>
      String((r as PodDisruptionBudget).status?.desiredHealthy ?? 0),
  },
  ageCol,
];

const endpointsliceColumns: Column<K8sResource>[] = [
  nameCol,
  namespaceCol,
  {
    key: "addressType",
    label: "Address Type",
    render: (r) => (r as EndpointSlice).addressType ?? "-",
  },
  {
    key: "ports",
    label: "Ports",
    render: (r) => {
      const ports = (r as EndpointSlice).ports;
      if (!ports?.length) return "-";
      return ports.map((p) => `${p.port ?? ""}/${p.protocol ?? "TCP"}`).join(
        ", ",
      );
    },
  },
  {
    key: "endpoints",
    label: "Endpoints",
    render: (r) => String((r as EndpointSlice).endpoints?.length ?? 0),
  },
  ageCol,
];

/** Maps API kind string to its column config. */
export const RESOURCE_COLUMNS: Record<string, Column<K8sResource>[]> = {
  pods: podColumns,
  deployments: deploymentColumns,
  statefulsets: statefulsetColumns,
  daemonsets: daemonsetColumns,
  services: serviceColumns,
  ingresses: ingressColumns,
  configmaps: configmapColumns,
  secrets: secretColumns,
  namespaces: namespaceColumns,
  nodes: nodeColumns,
  pvcs: pvcColumns,
  jobs: jobColumns,
  cronjobs: cronjobColumns,
  networkpolicies: networkpolicyColumns,
  roles: roleColumns,
  clusterroles: clusterroleColumns,
  rolebindings: rolebindingColumns,
  clusterrolebindings: clusterrolebindingColumns,
  replicasets: replicasetColumns,
  endpoints: endpointColumns,
  hpas: hpaColumns,
  pvs: pvColumns,
  storageclasses: storageclassColumns,
  resourcequotas: resourcequotaColumns,
  limitranges: limitrangeColumns,
  serviceaccounts: serviceaccountColumns,
  pdbs: pdbColumns,
  endpointslices: endpointsliceColumns,
  events: eventColumns,
  validatingwebhookconfigurations: validatingWebhookColumns,
  mutatingwebhookconfigurations: mutatingWebhookColumns,
};
