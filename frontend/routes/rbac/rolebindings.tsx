import { define } from "@/utils.ts";
import SubNav from "@/islands/SubNav.tsx";
import { DOMAIN_SECTIONS, flattenGroups } from "@/lib/constants.ts";
import ResourceTable from "@/islands/ResourceTable.tsx";

const section = DOMAIN_SECTIONS.find((s) => s.id === "security")!;

export default define.page(function RoleBindingsPage(ctx) {
  return (
    <>
      <SubNav tabs={flattenGroups(section)} currentPath={ctx.url.pathname} />
      <ResourceTable
        kind="rolebindings"
        title="RoleBindings"
        createHref="/rbac/rolebindings/new"
      />
    </>
  );
});
