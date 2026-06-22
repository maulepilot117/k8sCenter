import { define } from "@/utils.ts";
import ESOStoreDetail from "@/islands/ESOStoreDetail.tsx";

export default define.page(function ESStoreDetailPage(ctx) {
  const { namespace, name } = ctx.params;
  return <ESOStoreDetail namespace={namespace} name={name} />;
});
