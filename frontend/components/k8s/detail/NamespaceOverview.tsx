import type { K8sResource } from "@/lib/k8s-types.ts";
import type { Namespace } from "@/lib/k8s-types.ts";
import { statusColor } from "@/lib/status-colors.ts";

export function NamespaceOverview({ resource }: { resource: K8sResource }) {
  const ns = resource as Namespace;
  const phase = ns.status?.phase ?? "Active";

  return (
    <div class="space-y-4">
      <div>
        <h4 class="text-xs font-medium uppercase text-slate-500 dark:text-slate-400 mb-2">
          Summary
        </h4>
        <div>
          <div class="text-xs font-medium text-slate-500 dark:text-slate-400">
            Phase
          </div>
          <div class="mt-0.5">
            <span
              class={`inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium ring-1 ring-inset ${
                statusColor(phase)
              }`}
            >
              {phase}
            </span>
          </div>
        </div>
      </div>
    </div>
  );
}
