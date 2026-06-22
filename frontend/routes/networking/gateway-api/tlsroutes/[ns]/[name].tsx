import { define } from "@/utils.ts";
import GatewaySimpleRouteDetail from "@/islands/GatewaySimpleRouteDetail.tsx";

export default define.page(function TLSRouteDetailPage(ctx) {
  const { ns, name } = ctx.params;
  return (
    <GatewaySimpleRouteDetail kind="tlsroutes" namespace={ns} name={name} />
  );
});
