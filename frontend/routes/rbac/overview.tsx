import { define } from "@/utils.ts";
import SubNav from "@/islands/SubNav.tsx";
import { DOMAIN_SECTIONS, flattenGroups } from "@/lib/constants.ts";
import RBACOverview from "@/islands/RBACOverview.tsx";

const section = DOMAIN_SECTIONS.find((s) => s.id === "security")!;

export default define.page(function RBACOverviewPage(ctx) {
  return (
    <>
      <SubNav tabs={flattenGroups(section)} currentPath={ctx.url.pathname} />
      <RBACOverview />
    </>
  );
});
