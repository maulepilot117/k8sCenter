import { define } from "@/utils.ts";
import GatewayHTTPRouteDetail from "@/islands/GatewayHTTPRouteDetail.tsx";

export default define.page(function HTTPRouteDetailPage(ctx) {
  const { ns, name } = ctx.params;
  return <GatewayHTTPRouteDetail namespace={ns} name={name} />;
});
