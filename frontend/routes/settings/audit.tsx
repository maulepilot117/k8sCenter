import { define } from "@/utils.ts";
import SettingsDashboard from "@/islands/SettingsDashboard.tsx";

export default define.page(function SettingsAuditPage(ctx) {
  return <SettingsDashboard currentPath={ctx.url.pathname} />;
});
