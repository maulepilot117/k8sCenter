import type { ClusterRole, K8sResource } from "@/lib/k8s-types.ts";
import { RulesTable } from "./RulesTable.tsx";

export function ClusterRoleOverview({ resource }: { resource: K8sResource }) {
  const cr = resource as ClusterRole;
  return <RulesTable rules={cr.rules} />;
}
