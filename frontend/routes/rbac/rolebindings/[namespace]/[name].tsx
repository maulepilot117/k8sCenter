import { define } from "@/utils.ts";
import ResourceDetail from "@/islands/ResourceDetail.tsx";

export default define.page(function RoleBindingDetailPage(ctx) {
  return (
    <ResourceDetail
      kind="rolebindings"
      title="RoleBinding"
      namespace={ctx.params.namespace}
      name={ctx.params.name}
    />
  );
});
