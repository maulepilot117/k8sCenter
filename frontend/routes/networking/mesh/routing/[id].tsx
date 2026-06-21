import { define } from "@/utils.ts";
import SubNav from "@/islands/SubNav.tsx";
import { DOMAIN_SECTIONS, flattenGroups } from "@/lib/constants.ts";
import MeshRouteDetail from "@/islands/MeshRouteDetail.tsx";

const section = DOMAIN_SECTIONS.find((s) => s.id === "network")!;

export default define.page(function MeshRouteDetailPage(ctx) {
  const id = decodeURIComponent(ctx.params.id);
  return (
    <>
      <SubNav
        tabs={flattenGroups(section)}
        currentPath="/networking/mesh/routing"
      />
      <MeshRouteDetail id={id} />
    </>
  );
});
