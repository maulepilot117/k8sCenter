import type { K8sResource } from "@/lib/k8s-types.ts";
import type { StatefulSet } from "@/lib/k8s-types.ts";
import { KeyValueTable } from "./KeyValueTable.tsx";

export function StatefulSetOverview({ resource }: { resource: K8sResource }) {
  const s = resource as StatefulSet;
  const spec = s.spec;
  const status = s.status;

  // Extract update strategy from spec
  const updateStrategy = (spec as Record<string, unknown>).updateStrategy as
    | { type?: string; rollingUpdate?: { partition?: number } }
    | undefined;

  return (
    <div class="space-y-4">
      {/* Replicas */}
      <div>
        <h4 class="text-xs font-medium uppercase text-slate-500 dark:text-slate-400 mb-2">
          Replicas
        </h4>
        <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
          <div>
            <div class="text-xs font-medium text-slate-500 dark:text-slate-400">
              Desired
            </div>
            <div class="text-sm text-slate-900 dark:text-slate-100">
              {spec.replicas ?? 1}
            </div>
          </div>
          <div>
            <div class="text-xs font-medium text-slate-500 dark:text-slate-400">
              Ready
            </div>
            <div class="text-sm text-slate-900 dark:text-slate-100">
              {status.readyReplicas ?? 0}
            </div>
          </div>
          <div>
            <div class="text-xs font-medium text-slate-500 dark:text-slate-400">
              Current
            </div>
            <div class="text-sm text-slate-900 dark:text-slate-100">
              {status.currentReplicas ?? 0}
            </div>
          </div>
          <div>
            <div class="text-xs font-medium text-slate-500 dark:text-slate-400">
              Updated
            </div>
            <div class="text-sm text-slate-900 dark:text-slate-100">
              {status.updatedReplicas ?? 0}
            </div>
          </div>
        </div>
      </div>

      {/* Configuration */}
      <div>
        <h4 class="text-xs font-medium uppercase text-slate-500 dark:text-slate-400 mb-2">
          Configuration
        </h4>
        <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
          <div>
            <div class="text-xs font-medium text-slate-500 dark:text-slate-400">
              Service Name
            </div>
            <div class="text-sm text-slate-900 dark:text-slate-100">
              {spec.serviceName}
            </div>
          </div>
          {updateStrategy && (
            <>
              <div>
                <div class="text-xs font-medium text-slate-500 dark:text-slate-400">
                  Update Strategy
                </div>
                <div class="text-sm text-slate-900 dark:text-slate-100">
                  {updateStrategy.type ?? "RollingUpdate"}
                </div>
              </div>
              {updateStrategy.rollingUpdate?.partition != null && (
                <div>
                  <div class="text-xs font-medium text-slate-500 dark:text-slate-400">
                    Partition
                  </div>
                  <div class="text-sm text-slate-900 dark:text-slate-100">
                    {updateStrategy.rollingUpdate.partition}
                  </div>
                </div>
              )}
            </>
          )}
        </div>
      </div>

      {/* Selector */}
      {spec.selector?.matchLabels && (
        <KeyValueTable title="Selector" data={spec.selector.matchLabels} />
      )}
    </div>
  );
}
