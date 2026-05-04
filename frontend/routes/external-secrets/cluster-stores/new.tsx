import { define } from "@/utils.ts";
import SubNav from "@/islands/SubNav.tsx";
import { DOMAIN_SECTIONS } from "@/lib/constants.ts";
import SecretStoreWizard from "@/islands/SecretStoreWizard.tsx";

const section = DOMAIN_SECTIONS.find((s) => s.id === "external-secrets")!;

export default define.page(function ClusterSecretStoreNewPage() {
  return (
    <>
      <SubNav
        tabs={section.tabs ?? []}
        currentPath="/external-secrets/cluster-stores"
      />
      <SecretStoreWizard scope="cluster" />
    </>
  );
});
