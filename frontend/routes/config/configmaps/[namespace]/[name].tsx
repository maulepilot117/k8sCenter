import { define } from "@/utils.ts";
import ResourceDetail from "@/islands/ResourceDetail.tsx";

export default define.page(function ConfigMapDetailPage(ctx) {
  return (
    <ResourceDetail
      kind="configmaps"
      title="ConfigMap"
      namespace={ctx.params.namespace}
      name={ctx.params.name}
    />
  );
});
