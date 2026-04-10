import { define } from "@/utils.ts";
import SubNav from "@/islands/SubNav.tsx";
import NotificationRules from "@/islands/NotificationRules.tsx";

const tabs = [
  { label: "Feed", href: "/admin/notifications/feed" },
  { label: "Channels", href: "/admin/notifications/channels" },
  { label: "Rules", href: "/admin/notifications/rules" },
];

export default define.page(function NotificationRulesPage(ctx) {
  return (
    <>
      <SubNav tabs={tabs} currentPath={ctx.url.pathname} />
      <NotificationRules />
    </>
  );
});
