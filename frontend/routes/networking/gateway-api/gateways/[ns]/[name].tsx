import { define } from "@/utils.ts";
import GatewayDetail from "@/islands/GatewayDetail.tsx";

export default define.page(function GatewayDetailPage(ctx) {
  const { ns, name } = ctx.params;
  return <GatewayDetail namespace={ns} name={name} />;
});
