import { define } from "@/utils.ts";
import GatewaySimpleRouteDetail from "@/islands/GatewaySimpleRouteDetail.tsx";

export default define.page(function TCPRouteDetailPage(ctx) {
  const { ns, name } = ctx.params;
  return (
    <GatewaySimpleRouteDetail kind="tcproutes" namespace={ns} name={name} />
  );
});
