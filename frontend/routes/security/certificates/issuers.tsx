import { define } from "@/utils.ts";
import SubNav from "@/islands/SubNav.tsx";
import { DOMAIN_SECTIONS } from "@/lib/constants.ts";
import IssuersList from "@/islands/IssuersList.tsx";

const section = DOMAIN_SECTIONS.find((s) => s.id === "security")!;

export default define.page(function IssuersPage(ctx) {
  return (
    <>
      <SubNav tabs={section.tabs ?? []} currentPath={ctx.url.pathname} />
      <IssuersList />
    </>
  );
});
