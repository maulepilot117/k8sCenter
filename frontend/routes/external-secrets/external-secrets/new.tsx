import { define } from "@/utils.ts";
import ExternalSecretWizard from "@/islands/ExternalSecretWizard.tsx";

export default define.page(function ExternalSecretNewPage() {
  return <ExternalSecretWizard />;
});
