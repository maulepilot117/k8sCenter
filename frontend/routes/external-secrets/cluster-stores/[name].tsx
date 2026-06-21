import { define } from "@/utils.ts";
import SubNav from "@/islands/SubNav.tsx";
import { DOMAIN_SECTIONS, flattenGroups } from "@/lib/constants.ts";
import ESOClusterStoreDetail from "@/islands/ESOClusterStoreDetail.tsx";

const section = DOMAIN_SECTIONS.find((s) => s.id === "external-secrets")!;

export default define.page(function ESClusterStoreDetailPage(ctx) {
  const { name } = ctx.params;
  return (
    <>
      <SubNav
        tabs={flattenGroups(section)}
        currentPath="/external-secrets/cluster-stores"
      />
      <ESOClusterStoreDetail name={name} />
    </>
  );
});
