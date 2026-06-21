import { define } from "@/utils.ts";
import SubNav from "@/islands/SubNav.tsx";
import { DOMAIN_SECTIONS, flattenGroups } from "@/lib/constants.ts";
import GatewaySimpleRouteDetail from "@/islands/GatewaySimpleRouteDetail.tsx";

const section = DOMAIN_SECTIONS.find((s) => s.id === "network")!;

export default define.page(function TLSRouteDetailPage(ctx) {
  const { ns, name } = ctx.params;
  return (
    <>
      <SubNav
        tabs={flattenGroups(section)}
        currentPath="/networking/gateway-api"
      />
      <GatewaySimpleRouteDetail kind="tlsroutes" namespace={ns} name={name} />
    </>
  );
});
