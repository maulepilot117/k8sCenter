import { define } from "@/utils.ts";
import ResourceDetail from "@/islands/ResourceDetail.tsx";

export default define.page(function NamespaceDetailPage(ctx) {
  return (
    <ResourceDetail
      kind="namespaces"
      title="Namespace"
      name={ctx.params.name}
      clusterScoped
    />
  );
});
