import { define } from "@/utils.ts";
import ResourceTable from "@/islands/ResourceTable.tsx";

export default define.page(function PDBsPage() {
  return (
    <ResourceTable kind="poddisruptionbudgets" title="PodDisruptionBudgets" />
  );
});
