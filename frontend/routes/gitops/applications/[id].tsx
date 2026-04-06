import { define } from "@/utils.ts";
import SubNav from "@/islands/SubNav.tsx";
import { DOMAIN_SECTIONS } from "@/lib/constants.ts";
import GitOpsAppDetail from "@/islands/GitOpsAppDetail.tsx";

const section = DOMAIN_SECTIONS.find((s) => s.id === "gitops")!;

export default define.page(function GitOpsAppDetailPage(ctx) {
  const id = ctx.params.id;
  return (
    <>
      <SubNav tabs={section.tabs ?? []} currentPath="/gitops/applications" />
      <GitOpsAppDetail id={id} />
    </>
  );
});
