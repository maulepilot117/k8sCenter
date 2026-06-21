import { define } from "@/utils.ts";
import SubNav from "@/islands/SubNav.tsx";
import { DOMAIN_SECTIONS, flattenGroups } from "@/lib/constants.ts";
import ExternalSecretWizard from "@/islands/ExternalSecretWizard.tsx";

const section = DOMAIN_SECTIONS.find((s) => s.id === "external-secrets")!;

export default define.page(function ExternalSecretNewPage() {
  return (
    <>
      <SubNav
        tabs={flattenGroups(section)}
        currentPath="/external-secrets/external-secrets"
      />
      <ExternalSecretWizard />
    </>
  );
});
