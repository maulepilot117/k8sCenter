import { define } from "@/utils.ts";
import GatewayGRPCRouteDetail from "@/islands/GatewayGRPCRouteDetail.tsx";

export default define.page(function GRPCRouteDetailPage(ctx) {
  const { ns, name } = ctx.params;
  return <GatewayGRPCRouteDetail namespace={ns} name={name} />;
});
