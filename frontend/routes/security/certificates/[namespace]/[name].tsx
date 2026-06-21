import { define } from "@/utils.ts";
import SubNav from "@/islands/SubNav.tsx";
import { DOMAIN_SECTIONS, flattenGroups } from "@/lib/constants.ts";
import CertificateDetail from "@/islands/CertificateDetail.tsx";

const section = DOMAIN_SECTIONS.find((s) => s.id === "security")!;

export default define.page(function CertificateDetailPage(ctx) {
  const { namespace, name } = ctx.params;
  return (
    <>
      <SubNav
        tabs={flattenGroups(section)}
        currentPath="/security/certificates"
      />
      <CertificateDetail namespace={namespace} name={name} />
    </>
  );
});
