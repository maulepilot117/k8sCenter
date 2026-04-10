import { define } from "@/utils.ts";
import SubNav from "@/islands/SubNav.tsx";
import NotificationChannels from "@/islands/NotificationChannels.tsx";

const tabs = [
  { label: "Feed", href: "/admin/notifications/feed" },
  { label: "Channels", href: "/admin/notifications/channels" },
  { label: "Rules", href: "/admin/notifications/rules" },
];

export default define.page(function NotificationChannelsPage(ctx) {
  return (
    <>
      <SubNav tabs={tabs} currentPath={ctx.url.pathname} />
      <NotificationChannels />
    </>
  );
});
