import { define } from "@/utils.ts";
import SubNav from "@/islands/SubNav.tsx";
import { DOMAIN_SECTIONS, flattenGroups } from "@/lib/constants.ts";
import ESOStoreDetail from "@/islands/ESOStoreDetail.tsx";

const section = DOMAIN_SECTIONS.find((s) => s.id === "external-secrets")!;

export default define.page(function ESStoreDetailPage(ctx) {
  const { namespace, name } = ctx.params;
  return (
    <>
      <SubNav
        tabs={flattenGroups(section)}
        currentPath="/external-secrets/stores"
      />
      <ESOStoreDetail namespace={namespace} name={name} />
    </>
  );
});
