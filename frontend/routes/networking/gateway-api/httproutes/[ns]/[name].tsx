import { define } from "@/utils.ts";
import SubNav from "@/islands/SubNav.tsx";
import { DOMAIN_SECTIONS } from "@/lib/constants.ts";
import GatewayHTTPRouteDetail from "@/islands/GatewayHTTPRouteDetail.tsx";

const section = DOMAIN_SECTIONS.find((s) => s.id === "network")!;

export default define.page(function HTTPRouteDetailPage(ctx) {
  const { ns, name } = ctx.params;
  return (
    <>
      <SubNav
        tabs={section.tabs ?? []}
        currentPath="/networking/gateway-api"
      />
      <GatewayHTTPRouteDetail namespace={ns} name={name} />
    </>
  );
});
