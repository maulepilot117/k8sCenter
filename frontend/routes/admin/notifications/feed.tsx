import { define } from "@/utils.ts";
import SubNav from "@/islands/SubNav.tsx";
import NotificationFeed from "@/islands/NotificationFeed.tsx";
import { NOTIF_ADMIN_TABS } from "@/lib/notif-center-types.ts";

export default define.page(function NotificationFeedPage(ctx) {
  return (
    <>
      <SubNav tabs={NOTIF_ADMIN_TABS} currentPath={ctx.url.pathname} />
      <NotificationFeed />
    </>
  );
});
