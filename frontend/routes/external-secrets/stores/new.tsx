import { define } from "@/utils.ts";
import SecretStoreWizard from "@/islands/SecretStoreWizard.tsx";

export default define.page(function SecretStoreNewPage() {
  return <SecretStoreWizard scope="namespaced" />;
});
