import { define } from "@/utils.ts";
import IssuersList from "@/islands/IssuersList.tsx";

// ClusterIssuers and namespaced Issuers live on the same IssuersList today —
// this route exists so the IssuerWizard's "View Resource" link has a landing
// place scoped to cluster-issuer creations.
export default define.page(function ClusterIssuersPage() {
  return <IssuersList />;
});
