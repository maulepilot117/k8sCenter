import { define } from "@/utils.ts";
import SetupWizard from "@/islands/SetupWizard.tsx";

export default define.page(function SetupPage() {
  return (
    <div class="min-h-screen bg-slate-50 dark:bg-slate-900">
      <SetupWizard />
    </div>
  );
});
