import { ClusterRoleBindingOverview } from "@/components/k8s/detail/ClusterRoleBindingOverview.tsx";
import { ClusterRoleOverview } from "@/components/k8s/detail/ClusterRoleOverview.tsx";
import { ConfigMapOverview } from "@/components/k8s/detail/ConfigMapOverview.tsx";
import { CronJobOverview } from "@/components/k8s/detail/CronJobOverview.tsx";
import { DaemonSetOverview } from "@/components/k8s/detail/DaemonSetOverview.tsx";
import { DeploymentOverview } from "@/components/k8s/detail/DeploymentOverview.tsx";
import { IngressOverview } from "@/components/k8s/detail/IngressOverview.tsx";
import { JobOverview } from "@/components/k8s/detail/JobOverview.tsx";
import { NamespaceOverview } from "@/components/k8s/detail/NamespaceOverview.tsx";
import { NetworkPolicyOverview } from "@/components/k8s/detail/NetworkPolicyOverview.tsx";
import { NodeOverview } from "@/components/k8s/detail/NodeOverview.tsx";
import { PodOverview } from "@/components/k8s/detail/PodOverview.tsx";
import { PVCOverview } from "@/components/k8s/detail/PVCOverview.tsx";
import { RoleBindingOverview } from "@/components/k8s/detail/RoleBindingOverview.tsx";
import { RoleOverview } from "@/components/k8s/detail/RoleOverview.tsx";
import { SecretOverview } from "@/components/k8s/detail/SecretOverview.tsx";
import { StatefulSetOverview } from "@/components/k8s/detail/StatefulSetOverview.tsx";
import type { K8sResource } from "@/lib/k8s-types.ts";
import { ServiceOverview } from "@/src/components/k8s/detail/ServiceOverview.tsx";

function GenericOverview({ resource }: { resource: K8sResource }) {
  return (
    <div class="space-y-4">
      <p class="text-sm text-text-muted">
        No specialized overview available for this resource type.
      </p>
      <pre class="overflow-x-auto rounded-md border border-border-primary bg-surface p-3 text-xs font-mono text-text-secondary">
        {JSON.stringify(resource, null, 2)}
      </pre>
    </div>
  );
}

type OverviewComponent = (props: {
  resource: K8sResource;
}) => preact.JSX.Element;

const OVERVIEW_COMPONENTS: Record<string, OverviewComponent> = {
  deployments: DeploymentOverview,
  pods: PodOverview,
  services: ServiceOverview,
  nodes: NodeOverview,
  statefulsets: StatefulSetOverview,
  daemonsets: DaemonSetOverview,
  ingresses: IngressOverview,
  configmaps: ConfigMapOverview,
  secrets: SecretOverview,
  namespaces: NamespaceOverview,
  pvcs: PVCOverview,
  jobs: JobOverview,
  cronjobs: CronJobOverview,
  networkpolicies: NetworkPolicyOverview,
  roles: RoleOverview,
  clusterroles: ClusterRoleOverview,
  rolebindings: RoleBindingOverview,
  clusterrolebindings: ClusterRoleBindingOverview,
};

export function getOverviewComponent(kind: string): OverviewComponent {
  return OVERVIEW_COMPONENTS[kind] ?? GenericOverview;
}
