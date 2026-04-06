import { define } from "@/utils.ts";
import SubNav from "@/islands/SubNav.tsx";
import { DOMAIN_SECTIONS } from "@/lib/constants.ts";
import GitOpsApplications from "@/islands/GitOpsApplications.tsx";

const section = DOMAIN_SECTIONS.find((s) => s.id === "gitops")!;

export default define.page(function GitOpsApplicationsPage(ctx) {
  return (
    <>
      <SubNav tabs={section.tabs ?? []} currentPath={ctx.url.pathname} />
      <GitOpsApplications />
    </>
  );
});
