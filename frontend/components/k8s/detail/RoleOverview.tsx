import type { K8sResource, Role } from "@/lib/k8s-types.ts";
import { RulesTable } from "./RulesTable.tsx";

export function RoleOverview({ resource }: { resource: K8sResource }) {
  const r = resource as Role;
  return <RulesTable rules={r.rules} />;
}
