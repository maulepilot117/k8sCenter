import { define } from "@/utils.ts";
import SubNav from "@/islands/SubNav.tsx";
import { DOMAIN_SECTIONS, flattenGroups } from "@/lib/constants.ts";
import PolicyDashboard from "@/islands/PolicyDashboard.tsx";

const section = DOMAIN_SECTIONS.find((s) => s.id === "security")!;

export default define.page(function PoliciesPage(ctx) {
  return (
    <>
      <SubNav tabs={flattenGroups(section)} currentPath={ctx.url.pathname} />
      <PolicyDashboard />
    </>
  );
});
