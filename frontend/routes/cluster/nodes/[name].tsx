import { define } from "@/utils.ts";
import ResourceDetail from "@/islands/ResourceDetail.tsx";

export default define.page(function NodeDetailPage(ctx) {
  return (
    <ResourceDetail
      kind="nodes"
      title="Node"
      name={ctx.params.name}
      clusterScoped
    />
  );
});
