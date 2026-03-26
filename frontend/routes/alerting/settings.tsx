import { define } from "@/utils.ts";
import AlertSettings from "@/islands/AlertSettings.tsx";

export default define.page(function AlertSettingsPage() {
  return (
    <div class="space-y-6">
      <div>
        <h1 class="text-2xl font-bold text-text-primary">
          Alerting Settings
        </h1>
        <p class="mt-1 text-sm text-text-secondary">
          Configure SMTP email notifications and webhook integration
        </p>
      </div>
      <AlertSettings />
    </div>
  );
});
