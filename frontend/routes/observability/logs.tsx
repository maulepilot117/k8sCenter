import { define } from "@/utils.ts";
import LogExplorer from "@/islands/LogExplorer.tsx";

export default define.page(function LogsPage() {
  return (
    <div class="space-y-6">
      <div>
        <h1 class="text-2xl font-bold text-text-primary">Log Explorer</h1>
        <p class="mt-1 text-sm text-text-secondary">
          Search and stream logs from Loki
        </p>
      </div>
      <LogExplorer />
    </div>
  );
});
