import { define } from "@/utils.ts";
import ResourceTable from "@/islands/ResourceTable.tsx";

export default define.page(function RoleBindingsPage() {
  return (
    <ResourceTable
      kind="rolebindings"
      title="RoleBindings"
      createHref="/rbac/rolebindings/new"
    />
  );
});
