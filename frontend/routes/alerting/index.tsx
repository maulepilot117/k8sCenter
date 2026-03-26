import { define } from "@/utils.ts";
import AlertsPage from "@/islands/AlertsPage.tsx";

export default define.page(function AlertingPage() {
  return (
    <div class="space-y-6">
      <div>
        <h1 class="text-2xl font-bold text-text-primary">
          Alerts
        </h1>
        <p class="mt-1 text-sm text-text-secondary">
          Active alerts and alert history
        </p>
      </div>
      <AlertsPage />
    </div>
  );
});
