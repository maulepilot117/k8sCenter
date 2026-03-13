import { define } from "@/utils.ts";
import ResourceDetail from "@/islands/ResourceDetail.tsx";

export default define.page(function ClusterRoleDetailPage(ctx) {
  return (
    <ResourceDetail
      kind="clusterroles"
      title="ClusterRole"
      name={ctx.params.name}
      clusterScoped
    />
  );
});
