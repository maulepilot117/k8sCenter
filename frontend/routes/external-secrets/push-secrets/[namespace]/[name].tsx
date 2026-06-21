import { define } from "@/utils.ts";
import ESOPushSecretDetail from "@/islands/ESOPushSecretDetail.tsx";

export default define.page(function ESPushSecretDetailPage(ctx) {
  const { namespace, name } = ctx.params;
  return <ESOPushSecretDetail namespace={namespace} name={name} />;
});
