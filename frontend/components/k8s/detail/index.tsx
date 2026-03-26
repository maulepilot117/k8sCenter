import type { K8sResource } from"@/lib/k8s-types.ts";
import { DeploymentOverview } from"./DeploymentOverview.tsx";
import { PodOverview } from"./PodOverview.tsx";
import { ServiceOverview } from"./ServiceOverview.tsx";
import { NodeOverview } from"./NodeOverview.tsx";
import { StatefulSetOverview } from"./StatefulSetOverview.tsx";
import { DaemonSetOverview } from"./DaemonSetOverview.tsx";
import { IngressOverview } from"./IngressOverview.tsx";
import { ConfigMapOverview } from"./ConfigMapOverview.tsx";
import { SecretOverview } from"./SecretOverview.tsx";
import { NamespaceOverview } from"./NamespaceOverview.tsx";
import { PVCOverview } from"./PVCOverview.tsx";
import { JobOverview } from"./JobOverview.tsx";
import { CronJobOverview } from"./CronJobOverview.tsx";
import { NetworkPolicyOverview } from"./NetworkPolicyOverview.tsx";
import { RoleOverview } from"./RoleOverview.tsx";
import { ClusterRoleOverview } from"./ClusterRoleOverview.tsx";
import { RoleBindingOverview } from"./RoleBindingOverview.tsx";
import { ClusterRoleBindingOverview } from"./ClusterRoleBindingOverview.tsx";

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

type OverviewComponent = (
 props: { resource: K8sResource },
) => preact.JSX.Element;

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
