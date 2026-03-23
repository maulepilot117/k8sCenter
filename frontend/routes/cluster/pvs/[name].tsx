import { define } from "@/utils.ts";
import ResourceDetail from "@/islands/ResourceDetail.tsx";

export default define.page(function PVDetailPage(ctx) {
  return (
    <ResourceDetail
      kind="persistentvolumes"
      title="PersistentVolume"
      name={ctx.params.name}
      clusterScoped
    />
  );
});
