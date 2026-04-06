import { define } from "@/utils.ts";
import SubNav from "@/islands/SubNav.tsx";
import { DOMAIN_SECTIONS } from "@/lib/constants.ts";
import DiagnosticWorkspace from "@/islands/DiagnosticWorkspace.tsx";

const section = DOMAIN_SECTIONS.find((s) => s.id === "observability")!;

export default define.page(function InvestigatePage(ctx) {
  return (
    <>
      <SubNav tabs={section.tabs ?? []} currentPath={ctx.url.pathname} />
      <div class="p-6 space-y-6">
        <div>
          <h1 class="text-2xl font-bold text-text-primary">Investigate</h1>
          <p class="mt-1 text-sm text-text-secondary">
            Automated diagnostics and blast radius analysis
          </p>
        </div>
        <DiagnosticWorkspace />
      </div>
    </>
  );
});
