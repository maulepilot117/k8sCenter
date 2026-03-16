import { define } from "@/utils.ts";
import ResourceTable from "@/islands/ResourceTable.tsx";

export default define.page(function PersistentVolumesPage() {
  return <ResourceTable kind="pvs" title="PersistentVolumes" clusterScoped />;
});
