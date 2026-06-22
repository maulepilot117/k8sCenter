import { define } from "@/utils.ts";
import CertificateWizard from "@/islands/CertificateWizard.tsx";

export default define.page(function CertificateNewPage() {
  return (
    <CertificateWizard
      onClose={() => {
        globalThis.location.href = "/security/certificates";
      }}
    />
  );
});
