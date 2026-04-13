import { define } from "@/utils.ts";
import SubNav from "@/islands/SubNav.tsx";
import { DOMAIN_SECTIONS } from "@/lib/constants.ts";
import GatewaySimpleRouteDetail from "@/islands/GatewaySimpleRouteDetail.tsx";

const section = DOMAIN_SECTIONS.find((s) => s.id === "network")!;

export default define.page(function TCPRouteDetailPage(ctx) {
  const { ns, name } = ctx.params;
  return (
    <>
      <SubNav
        tabs={section.tabs ?? []}
        currentPath="/networking/gateway-api"
      />
      <GatewaySimpleRouteDetail kind="tcproutes" namespace={ns} name={name} />
    </>
  );
});
