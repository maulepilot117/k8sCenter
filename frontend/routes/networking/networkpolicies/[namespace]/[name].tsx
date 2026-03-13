import { define } from "@/utils.ts";
import ResourceDetail from "@/islands/ResourceDetail.tsx";

export default define.page(function NetworkPolicyDetailPage(ctx) {
  return (
    <ResourceDetail
      kind="networkpolicies"
      title="NetworkPolicy"
      namespace={ctx.params.namespace}
      name={ctx.params.name}
    />
  );
});
