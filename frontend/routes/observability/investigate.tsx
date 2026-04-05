import { define } from "@/utils.ts";
import DiagnosticWorkspace from "@/islands/DiagnosticWorkspace.tsx";

export default define.page(function InvestigatePage() {
  return (
    <div class="space-y-6">
      <div>
        <h1 class="text-2xl font-bold text-text-primary">Investigate</h1>
        <p class="mt-1 text-sm text-text-secondary">
          Automated diagnostics and blast radius analysis
        </p>
      </div>
      <DiagnosticWorkspace />
    </div>
  );
});
