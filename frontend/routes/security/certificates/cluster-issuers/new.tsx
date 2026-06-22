import { define } from "@/utils.ts";
import IssuerWizard from "@/islands/IssuerWizard.tsx";

export default define.page(function ClusterIssuerNewPage() {
  return (
    <IssuerWizard
      scope="cluster"
      onClose={() => {
        globalThis.location.href = "/security/certificates/cluster-issuers";
      }}
    />
  );
});
