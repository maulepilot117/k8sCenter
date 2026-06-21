import { define } from "@/utils.ts";
import SubNav from "@/islands/SubNav.tsx";
import { DOMAIN_SECTIONS, flattenGroups } from "@/lib/constants.ts";
import GitOpsAppDetail from "@/islands/GitOpsAppDetail.tsx";

const section = DOMAIN_SECTIONS.find((s) => s.id === "gitops")!;

export default define.page(function GitOpsAppDetailPage(ctx) {
  const id = decodeURIComponent(ctx.params.id);
  return (
    <>
      <SubNav
        tabs={flattenGroups(section)}
        currentPath="/gitops/applications"
      />
      <GitOpsAppDetail id={id} />
    </>
  );
});
