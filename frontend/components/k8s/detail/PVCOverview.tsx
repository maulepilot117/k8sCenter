import type { K8sResource } from "@/lib/k8s-types.ts";
import type { PersistentVolumeClaim } from "@/lib/k8s-types.ts";
import { statusColor } from "@/lib/status-colors.ts";

export function PVCOverview({ resource }: { resource: K8sResource }) {
  const pvc = resource as PersistentVolumeClaim;
  const spec = pvc.spec;
  const status = pvc.status;
  const phase = status?.phase ?? "Pending";

  // Volume name from spec.volumeName
  const volumeName = (spec as Record<string, unknown>).volumeName as
    | string
    | undefined;

  return (
    <div class="space-y-4">
      <div>
        <h4 class="text-xs font-medium uppercase text-slate-500 dark:text-slate-400 mb-2">
          Summary
        </h4>
        <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
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
          <div>
            <div class="text-xs font-medium text-slate-500 dark:text-slate-400">
              Storage Class
            </div>
            <div class="text-sm text-slate-900 dark:text-slate-100">
              {spec.storageClassName ?? "-"}
            </div>
          </div>
          <div>
            <div class="text-xs font-medium text-slate-500 dark:text-slate-400">
              Access Modes
            </div>
            <div class="text-sm text-slate-900 dark:text-slate-100">
              {spec.accessModes?.join(", ") ?? "-"}
            </div>
          </div>
          <div>
            <div class="text-xs font-medium text-slate-500 dark:text-slate-400">
              Requested Capacity
            </div>
            <div class="text-sm font-mono text-slate-900 dark:text-slate-100">
              {spec.resources?.requests?.storage ?? "-"}
            </div>
          </div>
          <div>
            <div class="text-xs font-medium text-slate-500 dark:text-slate-400">
              Actual Capacity
            </div>
            <div class="text-sm font-mono text-slate-900 dark:text-slate-100">
              {status?.capacity?.storage ?? "-"}
            </div>
          </div>
          <div>
            <div class="text-xs font-medium text-slate-500 dark:text-slate-400">
              Volume Name
            </div>
            <div class="text-sm font-mono text-slate-900 dark:text-slate-100 break-all">
              {volumeName ?? "-"}
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}
