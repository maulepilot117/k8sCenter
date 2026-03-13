import { define } from "@/utils.ts";
import ResourceDetail from "@/islands/ResourceDetail.tsx";

export default define.page(function PodDetailPage(ctx) {
  return (
    <ResourceDetail
      kind="pods"
      title="Pod"
      namespace={ctx.params.namespace}
      name={ctx.params.name}
    />
  );
});
