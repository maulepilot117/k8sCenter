import { define } from "@/utils.ts";
import ESOExternalSecretsList from "@/islands/ESOExternalSecretsList.tsx";

export default define.page(function ExternalSecretsPage() {
  return <ESOExternalSecretsList />;
});
