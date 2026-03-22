import { define } from "@/utils.ts";
import SettingsPage from "@/islands/SettingsPage.tsx";

export default define.page(function GeneralSettingsPage() {
  return (
    <div class="space-y-6">
      <div>
        <h1 class="text-2xl font-bold text-slate-900 dark:text-white">
          Settings
        </h1>
        <p class="mt-1 text-sm text-slate-500 dark:text-slate-400">
          Configure monitoring, alerting, and other application settings.
        </p>
      </div>
      <SettingsPage />
    </div>
  );
});
