import { define } from "@/utils.ts";
import ResourceDetail from "@/islands/ResourceDetail.tsx";

export default define.page(function SecretDetailPage(ctx) {
  return (
    <ResourceDetail
      kind="secrets"
      title="Secret"
      namespace={ctx.params.namespace}
      name={ctx.params.name}
    />
  );
});
