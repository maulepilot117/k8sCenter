import { define } from "@/utils.ts";
import AlertRulesPage from "@/islands/AlertRulesPage.tsx";

export default define.page(function AlertRulesRoute() {
  return (
    <div class="space-y-6">
      <div>
        <h1 class="text-2xl font-bold text-slate-900 dark:text-white">
          Alert Rules
        </h1>
        <p class="mt-1 text-sm text-slate-500 dark:text-slate-400">
          Manage PrometheusRule CRDs
        </p>
      </div>
      <AlertRulesPage />
    </div>
  );
});
