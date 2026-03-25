import { define } from "@/utils.ts";
import ResourceDetail from "@/islands/ResourceDetail.tsx";

export default define.page(function ServiceAccountDetailPage(ctx) {
  return (
    <ResourceDetail
      kind="serviceaccounts"
      title="ServiceAccount"
      namespace={ctx.params.namespace}
      name={ctx.params.name}
    />
  );
});
