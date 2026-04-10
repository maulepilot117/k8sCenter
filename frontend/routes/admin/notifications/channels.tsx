import { define } from "@/utils.ts";
import SubNav from "@/islands/SubNav.tsx";
import NotificationChannels from "@/islands/NotificationChannels.tsx";
import { NOTIF_ADMIN_TABS } from "@/lib/notif-center-types.ts";

export default define.page(function NotificationChannelsPage(ctx) {
  return (
    <>
      <SubNav tabs={NOTIF_ADMIN_TABS} currentPath={ctx.url.pathname} />
      <NotificationChannels />
    </>
  );
});
