import { define } from "@/utils.ts";
import ResourceDetail from "@/islands/ResourceDetail.tsx";

export default define.page(function RoleDetailPage(ctx) {
  return (
    <ResourceDetail
      kind="roles"
      title="Role"
      namespace={ctx.params.namespace}
      name={ctx.params.name}
    />
  );
});
