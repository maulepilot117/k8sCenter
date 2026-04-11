import { define } from "@/utils.ts";
import SubNav from "@/islands/SubNav.tsx";
import { DOMAIN_SECTIONS } from "@/lib/constants.ts";
import VulnerabilityDetail from "@/islands/VulnerabilityDetail.tsx";

const section = DOMAIN_SECTIONS.find((s) => s.id === "security")!;

export default define.page(function VulnerabilityDetailPage(ctx) {
  const { namespace, kind, name } = ctx.params;
  return (
    <>
      <SubNav
        tabs={section.tabs ?? []}
        currentPath="/security/vulnerabilities"
      />
      <VulnerabilityDetail
        namespace={namespace}
        kind={kind}
        name={name}
      />
    </>
  );
});
