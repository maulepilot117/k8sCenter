import { define } from "@/utils.ts";
import ResourceDetail from "@/islands/ResourceDetail.tsx";

export default define.page(function ServiceDetailPage(ctx) {
  return (
    <ResourceDetail
      kind="services"
      title="Service"
      namespace={ctx.params.namespace}
      name={ctx.params.name}
    />
  );
});
