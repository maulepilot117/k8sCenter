import { define } from "@/utils.ts";
import IssuerWizard from "@/islands/IssuerWizard.tsx";

export default define.page(function IssuerNewPage() {
  return <IssuerWizard scope="namespaced" />;
});
