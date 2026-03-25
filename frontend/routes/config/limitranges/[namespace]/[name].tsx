import { define } from "@/utils.ts";
import ResourceDetail from "@/islands/ResourceDetail.tsx";

export default define.page(function LimitRangeDetailPage(ctx) {
  return (
    <ResourceDetail
      kind="limitranges"
      title="LimitRange"
      namespace={ctx.params.namespace}
      name={ctx.params.name}
    />
  );
});
