import { define } from "@/utils.ts";
import ResourceDetail from "@/islands/ResourceDetail.tsx";

export default define.page(function PDBDetailPage(ctx) {
  return (
    <ResourceDetail
      kind="pdbs"
      title="PodDisruptionBudget"
      namespace={ctx.params.namespace}
      name={ctx.params.name}
    />
  );
});
