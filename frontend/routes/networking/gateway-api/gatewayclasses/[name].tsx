import { define } from "@/utils.ts";
import SubNav from "@/islands/SubNav.tsx";
import { DOMAIN_SECTIONS } from "@/lib/constants.ts";
import GatewayClassDetail from "@/islands/GatewayClassDetail.tsx";

const section = DOMAIN_SECTIONS.find((s) => s.id === "network")!;

export default define.page(function GatewayClassDetailPage(ctx) {
  const { name } = ctx.params;
  return (
    <>
      <SubNav
        tabs={section.tabs ?? []}
        currentPath="/networking/gateway-api"
      />
      <GatewayClassDetail name={name} />
    </>
  );
});
