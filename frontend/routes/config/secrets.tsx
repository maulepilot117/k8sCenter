import { define } from "@/utils.ts";
import ConfigDashboard from "@/islands/ConfigDashboard.tsx";

export default define.page(function SecretsPage(ctx) {
  return <ConfigDashboard currentPath={ctx.url.pathname} />;
});
