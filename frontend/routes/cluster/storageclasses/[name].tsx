import { define } from "@/utils.ts";
import ResourceDetail from "@/islands/ResourceDetail.tsx";

export default define.page(function StorageClassDetailPage(ctx) {
  return (
    <ResourceDetail
      kind="storageclasses"
      title="StorageClass"
      name={ctx.params.name}
      clusterScoped
    />
  );
});
