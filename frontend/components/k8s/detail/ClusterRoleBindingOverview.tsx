import type { K8sResource } from "@/lib/k8s-types.ts";
import { BindingDetail } from "./RoleBindingOverview.tsx";

export function ClusterRoleBindingOverview(
  { resource }: { resource: K8sResource },
) {
  return <BindingDetail resource={resource} />;
}
