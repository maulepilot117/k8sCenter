import { define } from "@/utils.ts";
import ResourceDetail from "@/islands/ResourceDetail.tsx";

export default define.page(function ClusterRoleBindingDetailPage(ctx) {
  return (
    <ResourceDetail
      kind="clusterrolebindings"
      title="ClusterRoleBinding"
      name={ctx.params.name}
      clusterScoped
    />
  );
});
