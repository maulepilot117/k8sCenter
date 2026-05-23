import { define } from "@/utils.ts";
import SubNav from "@/islands/SubNav.tsx";
import { DOMAIN_SECTIONS } from "@/lib/constants.ts";
import PromQLQuery from "@/islands/PromQLQuery.tsx";
import PromQLAdminGate from "@/islands/PromQLAdminGate.tsx";

const section = DOMAIN_SECTIONS.find((s) => s.id === "observability")!;

// F#4 — raw PromQL access is admin-only. The backend `/v1/monitoring/query`
// endpoint applies the same gate; the frontend gate here removes the empty
// query UI for non-admin operators so they land on the curated slug-based
// metrics surfaces instead. SSR can't read the auth state (the harness has
// no user context set server-side) — gating runs client-side via
// PromQLAdminGate, which renders the query island for admins and an
// explanatory redirect prompt for everyone else.
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
        <PromQLAdminGate>
          <PromQLQuery />
        </PromQLAdminGate>
      </div>
    </>
  );
});
