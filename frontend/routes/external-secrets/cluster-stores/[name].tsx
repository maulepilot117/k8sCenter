import { define } from "@/utils.ts";
import ESOClusterStoreDetail from "@/islands/ESOClusterStoreDetail.tsx";

export default define.page(function ESClusterStoreDetailPage(ctx) {
  const { name } = ctx.params;
  return <ESOClusterStoreDetail name={name} />;
});
