import { define } from "@/utils.ts";
import SubNav from "@/islands/SubNav.tsx";
import { DOMAIN_SECTIONS } from "@/lib/constants.ts";
import PromQLQuery from "@/islands/PromQLQuery.tsx";

const section = DOMAIN_SECTIONS.find((s) => s.id === "observability")!;

export default define.page(function PrometheusPage(ctx) {
  return (
    <>
      <SubNav tabs={section.tabs ?? []} currentPath={ctx.url.pathname} />
      <div class="p-6 space-y-6">
        <div>
          <h1 class="text-2xl font-bold text-text-primary">
            Prometheus Query
          </h1>
          <p class="mt-1 text-sm text-text-secondary">
            Run PromQL queries against the cluster's Prometheus instance
          </p>
        </div>
        <PromQLQuery />
      </div>
    </>
  );
});
