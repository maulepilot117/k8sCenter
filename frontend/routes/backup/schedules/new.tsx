import { define } from "@/utils.ts";
import SubNav from "@/islands/SubNav.tsx";
import { DOMAIN_SECTIONS } from "@/lib/constants.ts";
import VeleroScheduleWizard from "@/islands/VeleroScheduleWizard.tsx";

const section = DOMAIN_SECTIONS.find((s) => s.id === "backup")!;

export default define.page(function NewSchedulePage(ctx) {
  return (
    <>
      <SubNav tabs={section.tabs ?? []} currentPath={ctx.url.pathname} />
      <VeleroScheduleWizard />
    </>
  );
});
