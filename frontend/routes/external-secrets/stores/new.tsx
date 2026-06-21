import { define } from "@/utils.ts";
import SubNav from "@/islands/SubNav.tsx";
import { DOMAIN_SECTIONS, flattenGroups } from "@/lib/constants.ts";
import SecretStoreWizard from "@/islands/SecretStoreWizard.tsx";

const section = DOMAIN_SECTIONS.find((s) => s.id === "external-secrets")!;

export default define.page(function SecretStoreNewPage() {
  return (
    <>
      <SubNav
        tabs={flattenGroups(section)}
        currentPath="/external-secrets/stores"
      />
      <SecretStoreWizard scope="namespaced" />
    </>
  );
});
