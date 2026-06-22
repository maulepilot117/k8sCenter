import { define } from "@/utils.ts";
import SecretStoreWizard from "@/islands/SecretStoreWizard.tsx";

export default define.page(function ClusterSecretStoreNewPage() {
  return (
    <SecretStoreWizard
      scope="cluster"
      onClose={() => {
        globalThis.location.href = "/external-secrets/cluster-stores";
      }}
    />
  );
});
