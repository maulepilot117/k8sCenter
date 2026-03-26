import { define } from "@/utils.ts";
import WorkloadsDashboard from "@/islands/WorkloadsDashboard.tsx";

export default define.page(function WorkloadsPage(ctx) {
  return <WorkloadsDashboard currentPath={ctx.url.pathname} />;
});
