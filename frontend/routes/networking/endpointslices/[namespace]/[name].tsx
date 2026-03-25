import { define } from "@/utils.ts";
import ResourceDetail from "@/islands/ResourceDetail.tsx";

export default define.page(function EndpointSliceDetailPage(ctx) {
  return (
    <ResourceDetail
      kind="endpointslices"
      title="EndpointSlice"
      namespace={ctx.params.namespace}
      name={ctx.params.name}
    />
  );
});
