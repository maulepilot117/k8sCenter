import { define } from "@/utils.ts";
import ResourceTable from "@/islands/ResourceTable.tsx";

export default define.page(function StorageClassesPage() {
  return (
    <ResourceTable kind="storageclasses" title="StorageClasses" clusterScoped />
  );
});
