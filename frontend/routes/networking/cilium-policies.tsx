import { define } from "@/utils.ts";
import ResourceTable from "@/islands/ResourceTable.tsx";

export default define.page(function CiliumPoliciesPage() {
  return (
    <ResourceTable
      kind="ciliumnetworkpolicies"
      title="Cilium Network Policies"
      createHref="/networking/cilium-policies/new"
    />
  );
});
