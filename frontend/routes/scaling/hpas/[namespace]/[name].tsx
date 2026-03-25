import { define } from "@/utils.ts";
import ResourceDetail from "@/islands/ResourceDetail.tsx";

export default define.page(function HPADetailPage(ctx) {
  return (
    <ResourceDetail
      kind="hpas"
      title="HorizontalPodAutoscaler"
      namespace={ctx.params.namespace}
      name={ctx.params.name}
    />
  );
});
