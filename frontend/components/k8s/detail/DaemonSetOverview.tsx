import type { K8sResource } from "@/lib/k8s-types.ts";
import type { DaemonSet } from "@/lib/k8s-types.ts";
import { KeyValueTable } from "./KeyValueTable.tsx";

export function DaemonSetOverview({ resource }: { resource: K8sResource }) {
  const d = resource as DaemonSet;
  const spec = d.spec;
  const status = d.status;

  return (
    <div class="space-y-4">
      {/* Counts */}
      <div>
        <h4 class="text-xs font-medium uppercase text-slate-500 dark:text-slate-400 mb-2">
          Status
        </h4>
        <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
          <div>
            <div class="text-xs font-medium text-slate-500 dark:text-slate-400">
              Desired
            </div>
            <div class="text-sm text-slate-900 dark:text-slate-100">
              {status.desiredNumberScheduled}
            </div>
          </div>
          <div>
            <div class="text-xs font-medium text-slate-500 dark:text-slate-400">
              Current
            </div>
            <div class="text-sm text-slate-900 dark:text-slate-100">
              {status.currentNumberScheduled}
            </div>
          </div>
          <div>
            <div class="text-xs font-medium text-slate-500 dark:text-slate-400">
              Ready
            </div>
            <div class="text-sm text-slate-900 dark:text-slate-100">
              {status.numberReady}
            </div>
          </div>
          <div>
            <div class="text-xs font-medium text-slate-500 dark:text-slate-400">
              Available
            </div>
            <div class="text-sm text-slate-900 dark:text-slate-100">
              {status.numberAvailable ?? 0}
            </div>
          </div>
        </div>
      </div>

      {/* Selector */}
      {spec.selector?.matchLabels && (
        <KeyValueTable title="Selector" data={spec.selector.matchLabels} />
      )}
    </div>
  );
}
