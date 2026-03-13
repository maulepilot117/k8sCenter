import { define } from "@/utils.ts";
import ResourceDetail from "@/islands/ResourceDetail.tsx";

export default define.page(function StatefulSetDetailPage(ctx) {
  return (
    <ResourceDetail
      kind="statefulsets"
      title="StatefulSet"
      namespace={ctx.params.namespace}
      name={ctx.params.name}
    />
  );
});
