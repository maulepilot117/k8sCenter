import { define } from "@/utils.ts";
import AlertRulesIsland from "@/islands/AlertRulesPage.tsx";

export default define.page(function AlertRulesPage() {
  return (
    <div class="space-y-6">
      <div>
        <h1 class="text-2xl font-bold text-text-primary">
          Alert Rules
        </h1>
        <p class="mt-1 text-sm text-text-secondary">
          Manage PrometheusRule CRDs
        </p>
      </div>
      <AlertRulesIsland />
    </div>
  );
});
