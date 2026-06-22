import { define } from "@/utils.ts";
import ESOClusterExternalSecretDetail from "@/islands/ESOClusterExternalSecretDetail.tsx";

export default define.page(function ESClusterExternalSecretDetailPage(ctx) {
  const { name } = ctx.params;
  return <ESOClusterExternalSecretDetail name={name} />;
});
