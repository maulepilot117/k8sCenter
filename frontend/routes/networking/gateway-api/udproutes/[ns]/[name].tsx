import { define } from "@/utils.ts";
import GatewaySimpleRouteDetail from "@/islands/GatewaySimpleRouteDetail.tsx";

export default define.page(function UDPRouteDetailPage(ctx) {
  const { ns, name } = ctx.params;
  return (
    <GatewaySimpleRouteDetail kind="udproutes" namespace={ns} name={name} />
  );
});
