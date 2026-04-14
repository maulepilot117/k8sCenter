import { define } from "@/utils.ts";
import SubNav from "@/islands/SubNav.tsx";
import { DOMAIN_SECTIONS } from "@/lib/constants.ts";
import CertificateWizard from "@/islands/CertificateWizard.tsx";

const section = DOMAIN_SECTIONS.find((s) => s.id === "security")!;

export default define.page(function CertificateNewPage() {
  return (
    <>
      <SubNav
        tabs={section.tabs ?? []}
        currentPath="/security/certificates"
      />
      <CertificateWizard />
    </>
  );
});
