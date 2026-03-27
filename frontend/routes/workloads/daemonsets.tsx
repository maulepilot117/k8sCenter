import { define } from "@/utils.ts";
import WorkloadsDashboard from "@/islands/WorkloadsDashboard.tsx";

export default define.page(function WorkloadsSubPage(ctx) {
  return <WorkloadsDashboard currentPath={ctx.url.pathname} />;
});
