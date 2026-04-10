import { define } from "@/utils.ts";
import SubNav from "@/islands/SubNav.tsx";
import NotificationRules from "@/islands/NotificationRules.tsx";
import { NOTIF_ADMIN_TABS } from "@/lib/notif-center-types.ts";

export default define.page(function NotificationRulesPage(ctx) {
  return (
    <>
      <SubNav tabs={NOTIF_ADMIN_TABS} currentPath={ctx.url.pathname} />
      <NotificationRules />
    </>
  );
});
