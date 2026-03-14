import { define } from "@/utils.ts";
import AuthSettings from "@/islands/AuthSettings.tsx";

export default define.page(function AuthSettingsPage() {
  return (
    <div class="space-y-6">
      <div>
        <h1 class="text-2xl font-bold text-slate-900 dark:text-white">
          Authentication Settings
        </h1>
        <p class="mt-1 text-sm text-slate-500 dark:text-slate-400">
          Configure identity providers for single sign-on.
        </p>
      </div>
      <AuthSettings />
    </div>
  );
});
