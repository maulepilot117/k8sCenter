import { define } from "@/utils.ts";
import SubNav from "@/islands/SubNav.tsx";
import { DOMAIN_SECTIONS, flattenGroups } from "@/lib/constants.ts";
import IssuersList from "@/islands/IssuersList.tsx";

const section = DOMAIN_SECTIONS.find((s) => s.id === "security")!;

// ClusterIssuers and namespaced Issuers live on the same IssuersList today —
// this route exists so the IssuerWizard's "View Resource" link has a landing
// place scoped to cluster-issuer creations.
export default define.page(function ClusterIssuersPage(ctx) {
  return (
    <>
      <SubNav tabs={flattenGroups(section)} currentPath={ctx.url.pathname} />
      <IssuersList />
    </>
  );
});
