import { define } from "@/utils.ts";
import ResourceDetail from "@/islands/ResourceDetail.tsx";

export default define.page(function ReplicaSetDetailPage(ctx) {
  return (
    <ResourceDetail
      kind="replicasets"
      title="ReplicaSet"
      namespace={ctx.params.namespace}
      name={ctx.params.name}
    />
  );
});
