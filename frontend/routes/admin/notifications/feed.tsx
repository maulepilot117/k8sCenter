import { define } from "@/utils.ts";
import SubNav from "@/islands/SubNav.tsx";
import NotificationFeed from "@/islands/NotificationFeed.tsx";

const tabs = [
  { label: "Feed", href: "/admin/notifications/feed" },
  { label: "Channels", href: "/admin/notifications/channels" },
  { label: "Rules", href: "/admin/notifications/rules" },
];

export default define.page(function NotificationFeedPage(ctx) {
  return (
    <>
      <SubNav tabs={tabs} currentPath={ctx.url.pathname} />
      <NotificationFeed />
    </>
  );
});
