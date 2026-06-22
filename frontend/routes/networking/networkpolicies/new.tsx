import { define } from "@/utils.ts";
import NetworkPolicyWizard from "@/islands/NetworkPolicyWizard.tsx";

export default define.page(function NewNetworkPolicyPage() {
  return <NetworkPolicyWizard />;
});
