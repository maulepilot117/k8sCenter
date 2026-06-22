import { define } from "@/utils.ts";
import IngressWizard from "@/islands/IngressWizard.tsx";

export default define.page(function NewIngressPage() {
  return (
    <IngressWizard
      onClose={() => {
        globalThis.location.href = "/networking/ingresses";
      }}
    />
  );
});
