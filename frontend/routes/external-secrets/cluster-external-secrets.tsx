import { define } from "@/utils.ts";
import ESOClusterExternalSecretsList from "@/islands/ESOClusterExternalSecretsList.tsx";

export default define.page(function ClusterExternalSecretsPage() {
  return <ESOClusterExternalSecretsList />;
});
