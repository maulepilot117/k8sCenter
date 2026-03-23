import { define } from "@/utils.ts";
import ResourceTable from "@/islands/ResourceTable.tsx";

export default define.page(function NetworkPoliciesPage() {
  return <ResourceTable kind="networkpolicies" title="Network Policies" createHref="/networking/networkpolicies/new" />;
});
