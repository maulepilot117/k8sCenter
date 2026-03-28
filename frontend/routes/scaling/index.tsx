import { define } from "@/utils.ts";
import ResourceTable from "@/islands/ResourceTable.tsx";

export default define.page(function ScalingPage() {
  return (
    <div class="flex flex-col h-full">
      <div class="px-5 pt-4 pb-3">
        <h1 class="text-xl font-semibold tracking-tight text-text-primary">
          Scaling
        </h1>
        <p class="text-xs text-text-muted mt-0.5">
          Manage HorizontalPodAutoscalers and PodDisruptionBudgets
        </p>
      </div>
      <nav class="flex items-stretch border-b border-border-subtle bg-bg-surface px-4 shrink-0">
        <a
          href="/scaling/hpas"
          class="flex items-center px-3 py-2 text-xs font-medium text-accent no-underline border-b-2 border-accent whitespace-nowrap"
        >
          HPAs
        </a>
        <a
          href="/scaling/pdbs"
          class="flex items-center px-3 py-2 text-xs text-text-muted no-underline border-b-2 border-transparent whitespace-nowrap"
        >
          PDBs
        </a>
      </nav>
      <div class="flex-1 min-h-0 overflow-auto">
        <ResourceTable kind="horizontalpodautoscalers" title="HPAs" />
      </div>
    </div>
  );
});
