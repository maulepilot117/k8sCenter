import { define } from "@/utils.ts";
import ESOExternalSecretDetail from "@/islands/ESOExternalSecretDetail.tsx";

export default define.page(function ESExternalSecretDetailPage(ctx) {
  const { namespace, name } = ctx.params;
  return <ESOExternalSecretDetail namespace={namespace} name={name} />;
});
