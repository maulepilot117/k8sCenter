import { define } from "@/utils.ts";
import SubNav from "@/islands/SubNav.tsx";
import { DOMAIN_SECTIONS } from "@/lib/constants.ts";
import GatewayAPIDashboard from "@/islands/GatewayAPIDashboard.tsx";

const section = DOMAIN_SECTIONS.find((s) => s.id === "network")!;

export default define.page(function GatewayAPIPage(ctx) {
  return (
    <>
      <SubNav tabs={section.tabs ?? []} currentPath={ctx.url.pathname} />
      <GatewayAPIDashboard />
    </>
  );
});
