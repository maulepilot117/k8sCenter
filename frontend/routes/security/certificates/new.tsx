import { define } from "@/utils.ts";
import SubNav from "@/islands/SubNav.tsx";
import { DOMAIN_SECTIONS, flattenGroups } from "@/lib/constants.ts";
import CertificateWizard from "@/islands/CertificateWizard.tsx";

const section = DOMAIN_SECTIONS.find((s) => s.id === "security")!;

export default define.page(function CertificateNewPage() {
  return (
    <>
      <SubNav
        tabs={flattenGroups(section)}
        currentPath="/security/certificates"
      />
      <CertificateWizard />
    </>
  );
});
