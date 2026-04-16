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
  Namespace,
  NetworkPolicy,
  Node,
  PersistentVolume,
  PersistentVolumeClaim,
  Pod,
  PodDisruptionBudget,
  ReplicaSet,
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

// ---- Shared styled column helpers matching the mockup design ----

function styledName(name: string): ComponentChildren {
  return h("span", {
    style: {
      color: "var(--accent)",
      fontFamily: "var(--font-mono, monospace)",
      fontWeight: 500,
      fontSize: "13px",
    },
  }, name);
}

function styledNamespace(ns: string): ComponentChildren {
  return h("span", {
    style: {
      fontFamily: "var(--font-mono, monospace)",
      fontSize: "11px",
      padding: "2px 6px",
      background: "var(--bg-base)",
      borderRadius: "4px",
      color: "var(--text-secondary)",
    },
  }, ns);
}

function styledAge(timestamp: string): ComponentChildren {
  return h("span", {
    style: {
      fontFamily: "var(--font-mono, monospace)",
      fontSize: "12px",
      color: "var(--text-secondary)",
    },
  }, age(timestamp));
}

function styledBadge(
  text: string,
  status: "success" | "warning" | "error" | "info",
): ComponentChildren {
  const colors = {
    success: {
      dot: "var(--success)",
      bg: "var(--success-dim)",
      text: "var(--success)",
    },
    warning: {
      dot: "var(--warning)",
      bg: "var(--warning-dim)",
      text: "var(--warning)",
    },
    error: {
      dot: "var(--error)",
      bg: "var(--error-dim)",
      text: "var(--error)",
    },
    info: {
      dot: "var(--accent)",
      bg: "var(--accent-dim)",
      text: "var(--accent)",
    },
  };
  const c = colors[status];
  return h("span", {
    style: {
      display: "inline-flex",
      alignItems: "center",
      gap: "5px",
      padding: "3px 10px",
      borderRadius: "12px",
      fontSize: "11px",
      fontWeight: 500,
      background: c.bg,
      color: c.text,
    },
  }, [
    h("span", {
      style: {
        width: "6px",
        height: "6px",
        borderRadius: "50%",
        background: c.dot,
      },
    }),
    text,
  ]);
}

function styledReplicaDots(
  ready: number,
  desired: number,
  available?: number,
): ComponentChildren {
  const dots = [];
  for (let i = 0; i < desired; i++) {
    const isReady = i < ready;
    dots.push(h("div", {
      key: i,
      style: {
        width: "8px",
        height: "8px",
        borderRadius: "2px",
        background: isReady
          ? "var(--success)"
          : (available !== undefined && i < available
            ? "var(--warning)"
            : "var(--error)"),
      },
    }));
  }
  return h(
    "div",
    { style: { display: "flex", alignItems: "center", gap: "6px" } },
    [
      h("div", { style: { display: "flex", gap: "3px" } }, dots),
      h("span", {
        style: {
          fontSize: "12px",
          fontFamily: "var(--font-mono, monospace)",
          color: "var(--text-secondary)",
        },
      }, `${ready}/${desired}`),
    ],
  );
}

function styledImage(image: string): ComponentChildren {
  return h("span", {
    style: {
      fontFamily: "var(--font-mono, monospace)",
      fontSize: "11px",
      color: "var(--text-muted)",
      maxWidth: "200px",
      overflow: "hidden",
      textOverflow: "ellipsis",
      whiteSpace: "nowrap",
      display: "block",
    },
  }, image);
}

function styledMono(text: string): ComponentChildren {
  return h("span", {
    style: {
      fontFamily: "var(--font-mono, monospace)",
      fontSize: "12px",
      color: "var(--text-secondary)",
    },
  }, text);
}

// ---- Per-resource column sets ----

const podColumns: Column<K8sResource>[] = [
  {
    key: "name",
    label: "Name",
    sortable: true,
    render: (r) => styledName(r.metadata.name),
  },
  {
    key: "namespace",
    label: "Namespace",
    sortable: true,
    render: (r) => styledNamespace(r.metadata.namespace ?? "-"),
  },
  {
    key: "status",
    label: "Status",
    sortable: true,
    render: (r) => {
      const phase = (r as Pod).status?.phase ?? "Unknown";
      if (phase === "Running" || phase === "Succeeded") {
        return styledBadge(phase, "success");
      }
      if (phase === "Pending") return styledBadge(phase, "warning");
      return styledBadge(phase, "error");
    },
  },
  {
    key: "ready",
    label: "Ready",
    render: (r) => {
      const p = r as Pod;
      const containers = p.status?.containerStatuses ?? [];
      const readyCount = containers.filter((c) => c.ready).length;
      const total = containers.length || p.spec?.containers?.length || 0;
      return styledReplicaDots(readyCount, total);
    },
  },
  {
    key: "restarts",
    label: "Restarts",
    sortable: true,
    render: (r) => {
      const containers = (r as Pod).status?.containerStatuses ?? [];
      const count = containers.reduce((sum, c) => sum + c.restartCount, 0);
      return styledMono(String(count));
    },
  },
  {
    key: "node",
    label: "Node",
    render: (r) => styledMono((r as Pod).spec?.nodeName ?? "-"),
  },
  {
    key: "age",
    label: "Age",
    sortable: true,
    render: (r) => styledAge(r.metadata.creationTimestamp),
  },
];

const deploymentColumns: Column<K8sResource>[] = [
  {
    key: "name",
    label: "Name",
    sortable: true,
    render: (r) => styledName(r.metadata.name),
  },
  {
    key: "namespace",
    label: "Namespace",
    sortable: true,
    render: (r) => styledNamespace(r.metadata.namespace ?? "-"),
  },
  {
    key: "status",
    label: "Status",
    sortable: true,
    render: (r) => {
      const dep = r as Deployment;
      const available = dep.status?.availableReplicas ?? 0;
      const replicas = dep.spec?.replicas ?? 0;
      if (available === 0 && replicas > 0) {
        return styledBadge("Failed", "error");
      }
      if (available < replicas) return styledBadge("Progressing", "warning");
      return styledBadge("Running", "success");
    },
  },
  {
    key: "replicas",
    label: "Replicas",
    sortable: false,
    render: (r) => {
      const dep = r as Deployment;
      return styledReplicaDots(
        dep.status?.readyReplicas ?? 0,
        dep.spec?.replicas ?? 0,
        dep.status?.availableReplicas ?? 0,
      );
    },
  },
  {
    key: "image",
    label: "Image",
    sortable: false,
    render: (r) => {
      const dep = r as Deployment;
      return styledImage(
        dep.spec?.template?.spec?.containers?.[0]?.image ?? "-",
      );
    },
  },
  {
    key: "age",
    label: "Age",
    sortable: true,
    render: (r) => styledAge(r.metadata.creationTimestamp),
  },
];

const statefulsetColumns: Column<K8sResource>[] = [
  {
    key: "name",
    label: "Name",
    sortable: true,
    render: (r) => styledName(r.metadata.name),
  },
  {
    key: "namespace",
    label: "Namespace",
    sortable: true,
    render: (r) => styledNamespace(r.metadata.namespace ?? "-"),
  },
  {
    key: "status",
    label: "Status",
    sortable: true,
    render: (r) => {
      const s = r as StatefulSet;
      const ready = s.status?.readyReplicas ?? 0;
      const desired = s.spec?.replicas ?? 0;
      if (ready === 0 && desired > 0) return styledBadge("Failed", "error");
      if (ready < desired) return styledBadge("Progressing", "warning");
      return styledBadge("Running", "success");
    },
  },
  {
    key: "replicas",
    label: "Replicas",
    render: (r) => {
      const s = r as StatefulSet;
      return styledReplicaDots(
        s.status?.readyReplicas ?? 0,
        s.spec?.replicas ?? 0,
      );
    },
  },
  {
    key: "image",
    label: "Image",
    render: (r) => {
      const s = r as StatefulSet;
      return styledImage(
        s.spec?.template?.spec?.containers?.[0]?.image ?? "-",
      );
    },
  },
  {
    key: "age",
    label: "Age",
    sortable: true,
    render: (r) => styledAge(r.metadata.creationTimestamp),
  },
];

const daemonsetColumns: Column<K8sResource>[] = [
  {
    key: "name",
    label: "Name",
    sortable: true,
    render: (r) => styledName(r.metadata.name),
  },
  {
    key: "namespace",
    label: "Namespace",
    sortable: true,
    render: (r) => styledNamespace(r.metadata.namespace ?? "-"),
  },
  {
    key: "status",
    label: "Status",
    sortable: true,
    render: (r) => {
      const ds = r as DaemonSet;
      const ready = ds.status?.numberReady ?? 0;
      const desired = ds.status?.desiredNumberScheduled ?? 0;
      if (ready === 0 && desired > 0) return styledBadge("Failed", "error");
      if (ready < desired) return styledBadge("Progressing", "warning");
      return styledBadge("Running", "success");
    },
  },
  {
    key: "replicas",
    label: "Nodes Ready",
    render: (r) => {
      const ds = r as DaemonSet;
      return styledReplicaDots(
        ds.status?.numberReady ?? 0,
        ds.status?.desiredNumberScheduled ?? 0,
        ds.status?.numberAvailable ?? 0,
      );
    },
  },
  {
    key: "image",
    label: "Image",
    render: (r) => {
      const ds = r as DaemonSet;
      return styledImage(
        ds.spec?.template?.spec?.containers?.[0]?.image ?? "-",
      );
    },
  },
  {
    key: "age",
    label: "Age",
    sortable: true,
    render: (r) => styledAge(r.metadata.creationTimestamp),
  },
];

const serviceColumns: Column<K8sResource>[] = [
  {
    key: "name",
    label: "Name",
    sortable: true,
    render: (r) => styledName(r.metadata.name),
  },
  {
    key: "namespace",
    label: "Namespace",
    sortable: true,
    render: (r) => styledNamespace(r.metadata.namespace ?? "-"),
  },
  {
    key: "type",
    label: "Type",
    sortable: true,
    render: (r) => {
      const svcType = (r as Service).spec?.type ?? "ClusterIP";
      if (svcType === "ClusterIP") return styledBadge(svcType, "info");
      if (svcType === "NodePort") return styledBadge(svcType, "success");
      if (svcType === "LoadBalancer") return styledBadge(svcType, "info");
      return styledBadge(svcType, "warning");
    },
  },
  {
    key: "clusterIP",
    label: "Cluster IP",
    render: (r) => styledMono((r as Service).spec?.clusterIP ?? "-"),
  },
  {
    key: "ports",
    label: "Ports",
    render: (r) => {
      const ports = (r as Service).spec?.ports;
      if (!ports?.length) return styledMono("-");
      return styledMono(
        ports.map((p) => `${p.port}/${p.protocol ?? "TCP"}`).join(", "),
      );
    },
  },
  {
    key: "age",
    label: "Age",
    sortable: true,
    render: (r) => styledAge(r.metadata.creationTimestamp),
  },
];

const ingressColumns: Column<K8sResource>[] = [
  {
    key: "name",
    label: "Name",
    sortable: true,
    render: (r) => styledName(r.metadata.name),
  },
  {
    key: "namespace",
    label: "Namespace",
    sortable: true,
    render: (r) => styledNamespace(r.metadata.namespace ?? "-"),
  },
  {
    key: "hosts",
    label: "Hosts",
    render: (r) => {
      const rules = (r as Ingress).spec?.rules;
      if (!rules?.length) return styledMono("-");
      return styledMono(rules.map((rule) => rule.host ?? "*").join(", "));
    },
  },
  {
    key: "address",
    label: "Address",
    render: (r) => {
      const lb = (r as Ingress).status?.loadBalancer?.ingress;
      if (!lb?.length) return styledMono("-");
      return styledMono(
        lb.map((i) => i.ip ?? i.hostname ?? "").join(", "),
      );
    },
  },
  {
    key: "age",
    label: "Age",
    sortable: true,
    render: (r) => styledAge(r.metadata.creationTimestamp),
  },
];

const configmapColumns: Column<K8sResource>[] = [
  {
    key: "name",
    label: "Name",
    sortable: true,
    render: (r) => styledName(r.metadata.name),
  },
  {
    key: "namespace",
    label: "Namespace",
    sortable: true,
    render: (r) => styledNamespace(r.metadata.namespace ?? "-"),
  },
  {
    key: "keys",
    label: "Keys",
    render: (r) => {
      const data = (r as ConfigMap).data;
      return styledMono(String(data ? Object.keys(data).length : 0));
    },
  },
  {
    key: "age",
    label: "Age",
    sortable: true,
    render: (r) => styledAge(r.metadata.creationTimestamp),
  },
];

const secretColumns: Column<K8sResource>[] = [
  {
    key: "name",
    label: "Name",
    sortable: true,
    render: (r) => styledName(r.metadata.name),
  },
  {
    key: "namespace",
    label: "Namespace",
    sortable: true,
    render: (r) => styledNamespace(r.metadata.namespace ?? "-"),
  },
  {
    key: "type",
    label: "Type",
    sortable: true,
    render: (r) => {
      const t = (r as Secret).type ?? "Opaque";
      const status = t === "kubernetes.io/tls" ? "success" : "info";
      return styledBadge(t, status);
    },
  },
  {
    key: "keys",
    label: "Keys",
    render: (r) => {
      const data = (r as Secret).data;
      return styledMono(String(data ? Object.keys(data).length : 0));
    },
  },
  {
    key: "age",
    label: "Age",
    sortable: true,
    render: (r) => styledAge(r.metadata.creationTimestamp),
  },
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
  {
    key: "name",
    label: "Name",
    sortable: true,
    render: (r) => styledName(r.metadata.name),
  },
  {
    key: "namespace",
    label: "Namespace",
    sortable: true,
    render: (r) => styledNamespace(r.metadata.namespace ?? "-"),
  },
  {
    key: "status",
    label: "Status",
    sortable: true,
    render: (r) => {
      const phase = (r as PersistentVolumeClaim).status?.phase ?? "Pending";
      if (phase === "Bound") return styledBadge(phase, "success");
      if (phase === "Pending") return styledBadge(phase, "warning");
      return styledBadge(phase, "error");
    },
  },
  {
    key: "capacity",
    label: "Capacity",
    render: (r) => {
      const cap = (r as PersistentVolumeClaim).status?.capacity;
      return styledMono(cap?.storage ?? "-");
    },
  },
  {
    key: "storageClass",
    label: "Storage Class",
    render: (r) =>
      styledMono(
        (r as PersistentVolumeClaim).spec?.storageClassName ?? "-",
      ),
  },
  {
    key: "age",
    label: "Age",
    sortable: true,
    render: (r) => styledAge(r.metadata.creationTimestamp),
  },
];

const jobColumns: Column<K8sResource>[] = [
  {
    key: "name",
    label: "Name",
    sortable: true,
    render: (r) => styledName(r.metadata.name),
  },
  {
    key: "namespace",
    label: "Namespace",
    sortable: true,
    render: (r) => styledNamespace(r.metadata.namespace ?? "-"),
  },
  {
    key: "status",
    label: "Status",
    render: (r) => {
      const j = r as Job;
      if (j.status?.completionTime) return styledBadge("Complete", "success");
      if ((j.status?.failed ?? 0) > 0) return styledBadge("Failed", "error");
      if ((j.status?.active ?? 0) > 0) return styledBadge("Running", "info");
      return styledBadge("Pending", "warning");
    },
  },
  {
    key: "completions",
    label: "Completions",
    render: (r) => {
      const j = r as Job;
      return styledReplicaDots(
        j.status?.succeeded ?? 0,
        j.spec?.completions ?? 1,
      );
    },
  },
  {
    key: "age",
    label: "Age",
    sortable: true,
    render: (r) => styledAge(r.metadata.creationTimestamp),
  },
];

const cronjobColumns: Column<K8sResource>[] = [
  {
    key: "name",
    label: "Name",
    sortable: true,
    render: (r) => styledName(r.metadata.name),
  },
  {
    key: "namespace",
    label: "Namespace",
    sortable: true,
    render: (r) => styledNamespace(r.metadata.namespace ?? "-"),
  },
  {
    key: "schedule",
    label: "Schedule",
    render: (r) => styledMono((r as CronJob).spec?.schedule ?? "-"),
  },
  {
    key: "suspend",
    label: "Suspend",
    render: (r) => {
      const suspended = (r as CronJob).spec?.suspend;
      return suspended
        ? styledBadge("Suspended", "warning")
        : styledBadge("Active", "success");
    },
  },
  {
    key: "lastSchedule",
    label: "Last Schedule",
    render: (r) => {
      const t = (r as CronJob).status?.lastScheduleTime;
      return t ? styledAge(t) : styledMono("-");
    },
  },
  {
    key: "age",
    label: "Age",
    sortable: true,
    render: (r) => styledAge(r.metadata.creationTimestamp),
  },
];

const networkpolicyColumns: Column<K8sResource>[] = [
  {
    key: "name",
    label: "Name",
    sortable: true,
    render: (r) => styledName(r.metadata.name),
  },
  {
    key: "namespace",
    label: "Namespace",
    sortable: true,
    render: (r) => styledNamespace(r.metadata.namespace ?? "-"),
  },
  {
    key: "podSelector",
    label: "Applies To",
    render: (r) => {
      const labels = (r as NetworkPolicy).spec?.podSelector?.matchLabels;
      if (!labels || Object.keys(labels).length === 0) {
        return styledMono("All pods");
      }
      const text = Object.entries(labels).map(([k, v]) => `${k}=${v}`).join(
        ", ",
      );
      return h("span", {
        style: {
          fontFamily: "var(--font-mono, monospace)",
          fontSize: "12px",
          color: "var(--text-secondary)",
          maxWidth: "200px",
          overflow: "hidden",
          textOverflow: "ellipsis",
          whiteSpace: "nowrap",
          display: "block",
        },
      }, text);
    },
  },
  {
    key: "policyTypes",
    label: "Policy Types",
    render: (r) => {
      const types = (r as NetworkPolicy).spec?.policyTypes ?? ["Ingress"];
      return h(
        "span",
        { style: { display: "flex", gap: "4px" } },
        types.map(
          (t) => styledBadge(t, t === "Ingress" ? "info" : "warning"),
        ),
      );
    },
  },
  {
    key: "rules",
    label: "Rules",
    render: (r) => {
      const spec = (r as NetworkPolicy).spec;
      const ing = spec?.ingress?.length ?? 0;
      const egr = spec?.egress?.length ?? 0;
      const total = ing + egr;
      return styledMono(String(total));
    },
  },
  {
    key: "age",
    label: "Age",
    sortable: true,
    render: (r) => styledAge(r.metadata.creationTimestamp),
  },
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
  {
    key: "name",
    label: "Name",
    sortable: true,
    render: (r) => styledName(r.metadata.name),
  },
  {
    key: "namespace",
    label: "Namespace",
    sortable: true,
    render: (r) => styledNamespace(r.metadata.namespace ?? "-"),
  },
  {
    key: "status",
    label: "Status",
    render: (r) => {
      const rs = r as ReplicaSet;
      const ready = rs.status?.readyReplicas ?? 0;
      const desired = rs.spec?.replicas ?? 0;
      if (ready === 0 && desired > 0) return styledBadge("Failed", "error");
      if (ready < desired) return styledBadge("Progressing", "warning");
      return styledBadge("Running", "success");
    },
  },
  {
    key: "replicas",
    label: "Replicas",
    render: (r) => {
      const rs = r as ReplicaSet;
      return styledReplicaDots(
        rs.status?.readyReplicas ?? 0,
        rs.spec?.replicas ?? 0,
      );
    },
  },
  {
    key: "age",
    label: "Age",
    sortable: true,
    render: (r) => styledAge(r.metadata.creationTimestamp),
  },
];

const endpointColumns: Column<K8sResource>[] = [
  {
    key: "name",
    label: "Name",
    sortable: true,
    render: (r) => styledName(r.metadata.name),
  },
  {
    key: "namespace",
    label: "Namespace",
    sortable: true,
    render: (r) => styledNamespace(r.metadata.namespace ?? "-"),
  },
  {
    key: "addresses",
    label: "Addresses",
    render: (r) => {
      const ep = r as Endpoints;
      const count = ep.subsets?.reduce(
        (sum, s) => sum + (s.addresses?.length ?? 0),
        0,
      ) ?? 0;
      return styledMono(String(count));
    },
  },
  {
    key: "age",
    label: "Age",
    sortable: true,
    render: (r) => styledAge(r.metadata.creationTimestamp),
  },
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
  {
    key: "name",
    label: "Name",
    sortable: true,
    render: (r) => styledName(r.metadata.name),
  },
  {
    key: "capacity",
    label: "Capacity",
    render: (r) =>
      styledMono((r as PersistentVolume).spec?.capacity?.storage ?? "-"),
  },
  {
    key: "accessModes",
    label: "Access Modes",
    render: (r) =>
      styledMono(
        (r as PersistentVolume).spec?.accessModes?.join(", ") ?? "-",
      ),
  },
  {
    key: "reclaimPolicy",
    label: "Reclaim Policy",
    render: (r) =>
      styledMono(
        (r as PersistentVolume).spec?.persistentVolumeReclaimPolicy ?? "-",
      ),
  },
  {
    key: "status",
    label: "Status",
    sortable: true,
    render: (r) => {
      const phase = (r as PersistentVolume).status?.phase ?? "Available";
      if (phase === "Available" || phase === "Bound") {
        return styledBadge(phase, "success");
      }
      if (phase === "Released") return styledBadge(phase, "warning");
      return styledBadge(phase, "error");
    },
  },
  {
    key: "storageClass",
    label: "Storage Class",
    render: (r) =>
      styledMono((r as PersistentVolume).spec?.storageClassName ?? "-"),
  },
  {
    key: "claim",
    label: "Claim",
    render: (r) => {
      const ref = (r as PersistentVolume).spec?.claimRef;
      return styledMono(ref ? `${ref.namespace}/${ref.name}` : "-");
    },
  },
  {
    key: "age",
    label: "Age",
    sortable: true,
    render: (r) => styledAge(r.metadata.creationTimestamp),
  },
];

const storageclassColumns: Column<K8sResource>[] = [
  {
    key: "name",
    label: "Name",
    sortable: true,
    render: (r) => styledName(r.metadata.name),
  },
  {
    key: "provisioner",
    label: "Provisioner",
    render: (r) => styledMono((r as StorageClass).provisioner ?? "-"),
  },
  {
    key: "reclaimPolicy",
    label: "Reclaim Policy",
    render: (r) => {
      const policy = (r as StorageClass).reclaimPolicy ?? "-";
      if (policy === "Delete") return styledBadge(policy, "warning");
      if (policy === "Retain") return styledBadge(policy, "success");
      return styledMono(policy);
    },
  },
  {
    key: "volumeBindingMode",
    label: "Volume Binding Mode",
    render: (r) => styledMono((r as StorageClass).volumeBindingMode ?? "-"),
  },
  {
    key: "age",
    label: "Age",
    sortable: true,
    render: (r) => styledAge(r.metadata.creationTimestamp),
  },
];

const resourcequotaColumns: Column<K8sResource>[] = [
  {
    key: "name",
    label: "Name",
    sortable: true,
    render: (r) => styledName(r.metadata.name),
  },
  {
    key: "namespace",
    label: "Namespace",
    sortable: true,
    render: (r) => styledNamespace(r.metadata.namespace ?? "-"),
  },
  {
    key: "age",
    label: "Age",
    sortable: true,
    render: (r) => styledAge(r.metadata.creationTimestamp),
  },
];

const limitrangeColumns: Column<K8sResource>[] = [
  {
    key: "name",
    label: "Name",
    sortable: true,
    render: (r) => styledName(r.metadata.name),
  },
  {
    key: "namespace",
    label: "Namespace",
    sortable: true,
    render: (r) => styledNamespace(r.metadata.namespace ?? "-"),
  },
  {
    key: "age",
    label: "Age",
    sortable: true,
    render: (r) => styledAge(r.metadata.creationTimestamp),
  },
];

const serviceaccountColumns: Column<K8sResource>[] = [
  {
    key: "name",
    label: "Name",
    sortable: true,
    render: (r) => styledName(r.metadata.name),
  },
  {
    key: "namespace",
    label: "Namespace",
    sortable: true,
    render: (r) => styledNamespace(r.metadata.namespace ?? "-"),
  },
  {
    key: "secrets",
    label: "Secrets",
    render: (r) =>
      styledMono(String((r as ServiceAccount).secrets?.length ?? 0)),
  },
  {
    key: "age",
    label: "Age",
    sortable: true,
    render: (r) => styledAge(r.metadata.creationTimestamp),
  },
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
  {
    key: "name",
    label: "Name",
    sortable: true,
    render: (r) => styledName(r.metadata.name),
  },
  {
    key: "namespace",
    label: "Namespace",
    sortable: true,
    render: (r) => styledNamespace(r.metadata.namespace ?? "-"),
  },
  {
    key: "addressType",
    label: "Address Type",
    render: (r) =>
      styledBadge(
        (r as EndpointSlice).addressType ?? "-",
        "info",
      ),
  },
  {
    key: "ports",
    label: "Ports",
    render: (r) => {
      const ports = (r as EndpointSlice).ports;
      if (!ports?.length) return styledMono("-");
      return styledMono(
        ports.map((p) => `${p.port ?? ""}/${p.protocol ?? "TCP"}`).join(", "),
      );
    },
  },
  {
    key: "endpoints",
    label: "Endpoints",
    render: (r) =>
      styledMono(String((r as EndpointSlice).endpoints?.length ?? 0)),
  },
  {
    key: "age",
    label: "Age",
    sortable: true,
    render: (r) => styledAge(r.metadata.creationTimestamp),
  },
];

const ciliumnetworkpolicyColumns: Column<K8sResource>[] = [
  nameCol,
  namespaceCol,
  {
    key: "endpointSelector",
    label: "Endpoint Selector",
    render: (r) => {
      const spec = (r as K8sResource & {
        spec?: {
          endpointSelector?: { matchLabels?: Record<string, string> };
        };
      }).spec;
      const labels = spec?.endpointSelector?.matchLabels;
      if (!labels || Object.keys(labels).length === 0) return "All";
      return Object.entries(labels).map(([k, v]) => `${k}=${v}`).join(", ");
    },
    class: "max-w-xs truncate",
  },
  {
    key: "rules",
    label: "Rules",
    render: (r) => {
      const spec = (r as K8sResource & {
        spec?: {
          ingress?: unknown[];
          ingressDeny?: unknown[];
          egress?: unknown[];
          egressDeny?: unknown[];
        };
      }).spec;
      const count = (spec?.ingress?.length ?? 0) +
        (spec?.ingressDeny?.length ?? 0) +
        (spec?.egress?.length ?? 0) + (spec?.egressDeny?.length ?? 0);
      return String(count);
    },
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
  ciliumnetworkpolicies: ciliumnetworkpolicyColumns,
};
