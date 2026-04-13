import { define } from "@/utils.ts";
import SubNav from "@/islands/SubNav.tsx";
import { DOMAIN_SECTIONS } from "@/lib/constants.ts";
import GatewayGRPCRouteDetail from "@/islands/GatewayGRPCRouteDetail.tsx";

const section = DOMAIN_SECTIONS.find((s) => s.id === "network")!;

export default define.page(function GRPCRouteDetailPage(ctx) {
  const { ns, name } = ctx.params;
  return (
    <>
      <SubNav
        tabs={section.tabs ?? []}
        currentPath="/networking/gateway-api"
      />
      <GatewayGRPCRouteDetail namespace={ns} name={name} />
    </>
  );
});
