import { define } from "@/utils.ts";
import ResourceDetail from "@/islands/ResourceDetail.tsx";

export default define.page(function CiliumPolicyDetailPage(ctx) {
  return (
    <ResourceDetail
      kind="ciliumnetworkpolicies"
      title="CiliumNetworkPolicy"
      namespace={ctx.params.namespace}
      name={ctx.params.name}
    />
  );
});
