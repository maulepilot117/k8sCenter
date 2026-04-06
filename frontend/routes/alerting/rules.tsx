import { define } from "@/utils.ts";
import SubNav from "@/islands/SubNav.tsx";
import { DOMAIN_SECTIONS } from "@/lib/constants.ts";
import AlertRulesIsland from "@/islands/AlertRulesPage.tsx";

const section = DOMAIN_SECTIONS.find((s) => s.id === "observability")!;

export default define.page(function AlertRulesPage(ctx) {
  return (
    <>
      <SubNav tabs={section.tabs ?? []} currentPath={ctx.url.pathname} />
      <div class="p-6 space-y-6">
        <div>
          <h1 class="text-2xl font-bold text-text-primary">Alert Rules</h1>
          <p class="mt-1 text-sm text-text-secondary">
            Manage PrometheusRule CRDs
          </p>
        </div>
        <AlertRulesIsland />
      </div>
    </>
  );
});
