import { define } from "@/utils.ts";
import SubNav from "@/islands/SubNav.tsx";
import { DOMAIN_SECTIONS } from "@/lib/constants.ts";
import GatewayDetail from "@/islands/GatewayDetail.tsx";

const section = DOMAIN_SECTIONS.find((s) => s.id === "network")!;

export default define.page(function GatewayDetailPage(ctx) {
  const { ns, name } = ctx.params;
  return (
    <>
      <SubNav
        tabs={section.tabs ?? []}
        currentPath="/networking/gateway-api"
      />
      <GatewayDetail namespace={ns} name={name} />
    </>
  );
});
