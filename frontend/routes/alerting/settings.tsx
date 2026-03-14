import { define } from "@/utils.ts";
import AlertSettings from "@/islands/AlertSettings.tsx";

export default define.page(function AlertSettingsRoute() {
  return (
    <div class="space-y-6">
      <div>
        <h1 class="text-2xl font-bold text-slate-900 dark:text-white">
          Alerting Settings
        </h1>
        <p class="mt-1 text-sm text-slate-500 dark:text-slate-400">
          Configure SMTP email notifications and webhook integration
        </p>
      </div>
      <AlertSettings />
    </div>
  );
});
