import { define } from "@/utils.ts";
import SetupWizard from "@/islands/SetupWizard.tsx";

export default define.page(function SetupPage() {
  return (
    <div class="min-h-screen bg-base">
      <SetupWizard />
    </div>
  );
});
