import { define } from "@/utils.ts";
import SubNav from "@/islands/SubNav.tsx";
import { DOMAIN_SECTIONS } from "@/lib/constants.ts";
import VeleroBackupWizard from "@/islands/VeleroBackupWizard.tsx";

const section = DOMAIN_SECTIONS.find((s) => s.id === "backup")!;

export default define.page(function NewBackupPage(ctx) {
  return (
    <>
      <SubNav tabs={section.tabs ?? []} currentPath={ctx.url.pathname} />
      <VeleroBackupWizard />
    </>
  );
});
