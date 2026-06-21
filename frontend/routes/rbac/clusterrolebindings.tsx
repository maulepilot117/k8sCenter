import { define } from "@/utils.ts";
import ResourceTable from "@/islands/ResourceTable.tsx";

export default define.page(function ClusterRoleBindingsPage() {
  return (
    <ResourceTable
      kind="clusterrolebindings"
      title="ClusterRoleBindings"
      clusterScoped
      createHref="/rbac/clusterrolebindings/new"
    />
  );
});
