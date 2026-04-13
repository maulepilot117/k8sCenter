import { define } from "@/utils.ts";
import SubNav from "@/islands/SubNav.tsx";
import { DOMAIN_SECTIONS } from "@/lib/constants.ts";
import GitOpsAppSetDetail from "@/islands/GitOpsAppSetDetail.tsx";

const section = DOMAIN_SECTIONS.find((s) => s.id === "gitops")!;

export default define.page(function AppSetDetailPage(ctx) {
  const id = decodeURIComponent(ctx.params.id);
  return (
    <>
      <SubNav
        tabs={section.tabs ?? []}
        currentPath="/gitops/applicationsets"
      />
      <GitOpsAppSetDetail id={id} />
    </>
  );
});
