import { define } from "@/utils.ts";
import SubNav from "@/islands/SubNav.tsx";
import { DOMAIN_SECTIONS, flattenGroups } from "@/lib/constants.ts";
import VeleroRestoreWizard from "@/islands/VeleroRestoreWizard.tsx";

const section = DOMAIN_SECTIONS.find((s) => s.id === "backup")!;

export default define.page(function NewRestorePage(ctx) {
  return (
    <>
      <SubNav tabs={flattenGroups(section)} currentPath={ctx.url.pathname} />
      <VeleroRestoreWizard />
    </>
  );
});
