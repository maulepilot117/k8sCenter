import { define } from "@/utils.ts";
import ResourceDetail from "@/islands/ResourceDetail.tsx";

export default define.page(function EndpointDetailPage(ctx) {
  return (
    <ResourceDetail
      kind="endpoints"
      title="Endpoints"
      namespace={ctx.params.namespace}
      name={ctx.params.name}
    />
  );
});
