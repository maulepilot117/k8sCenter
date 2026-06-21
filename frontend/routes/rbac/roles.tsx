import { define } from "@/utils.ts";
import ResourceTable from "@/islands/ResourceTable.tsx";

export default define.page(function RolesPage() {
  return <ResourceTable kind="roles" title="Roles" />;
});
