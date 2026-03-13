import { define } from "@/utils.ts";
import ResourceDetail from "@/islands/ResourceDetail.tsx";

export default define.page(function IngressDetailPage(ctx) {
  return (
    <ResourceDetail
      kind="ingresses"
      title="Ingress"
      namespace={ctx.params.namespace}
      name={ctx.params.name}
    />
  );
});
