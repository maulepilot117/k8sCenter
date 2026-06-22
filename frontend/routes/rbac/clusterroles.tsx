import { define } from "@/utils.ts";
import ResourceTable from "@/islands/ResourceTable.tsx";

export default define.page(function ClusterRolesPage() {
  return (
    <ResourceTable kind="clusterroles" title="ClusterRoles" clusterScoped />
  );
});
