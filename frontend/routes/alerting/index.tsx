import { define } from "@/utils.ts";
import AlertsPage from "@/islands/AlertsPage.tsx";

export default define.page(function AlertingPage() {
  return (
    <div class="space-y-6">
      <div>
        <h1 class="text-2xl font-bold text-slate-900 dark:text-white">
          Alerts
        </h1>
        <p class="mt-1 text-sm text-slate-500 dark:text-slate-400">
          Active alerts and alert history
        </p>
      </div>
      <AlertsPage />
    </div>
  );
});
