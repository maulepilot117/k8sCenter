import { define } from "@/utils.ts";
import ResourceDetail from "@/islands/ResourceDetail.tsx";

export default define.page(function PVCDetailPage(ctx) {
  return (
    <ResourceDetail
      kind="pvcs"
      title="PersistentVolumeClaim"
      namespace={ctx.params.namespace}
      name={ctx.params.name}
    />
  );
});
