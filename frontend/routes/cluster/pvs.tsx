import { define } from "@/utils.ts";
import StorageDashboard from "@/islands/StorageDashboard.tsx";

export default define.page(function PVsPage(ctx) {
  return <StorageDashboard currentPath={ctx.url.pathname} />;
});
