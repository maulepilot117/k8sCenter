import { define } from "@/utils.ts";
import NetworkingDashboard from "@/islands/NetworkingDashboard.tsx";

export default define.page(function NetworkFlowsPage(ctx) {
  return <NetworkingDashboard currentPath={ctx.url.pathname} />;
});
