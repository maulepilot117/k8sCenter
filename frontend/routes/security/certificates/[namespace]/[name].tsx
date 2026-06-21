import { define } from "@/utils.ts";
import CertificateDetail from "@/islands/CertificateDetail.tsx";

export default define.page(function CertificateDetailPage(ctx) {
  const { namespace, name } = ctx.params;
  return <CertificateDetail namespace={namespace} name={name} />;
});
