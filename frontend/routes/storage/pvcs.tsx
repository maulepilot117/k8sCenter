import { define } from "@/utils.ts";
import ResourceTable from "@/islands/ResourceTable.tsx";

export default define.page(function PVCsPage() {
  return (
    <ResourceTable
      kind="pvcs"
      title="Persistent Volume Claims"
      createHref="/storage/pvcs/new"
    />
  );
});
