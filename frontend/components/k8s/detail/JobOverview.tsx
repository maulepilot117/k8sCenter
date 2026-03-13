import type { K8sResource } from "@/lib/k8s-types.ts";
import type { Job } from "@/lib/k8s-types.ts";
import { age } from "@/lib/format.ts";
import { ConditionsTable } from "./ConditionsTable.tsx";

export function JobOverview({ resource }: { resource: K8sResource }) {
  const j = resource as Job;
  const spec = j.spec;
  const status = j.status;

  return (
    <div class="space-y-4">
      {/* Configuration */}
      <div>
        <h4 class="text-xs font-medium uppercase text-slate-500 dark:text-slate-400 mb-2">
          Configuration
        </h4>
        <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
          <div>
            <div class="text-xs font-medium text-slate-500 dark:text-slate-400">
              Completions
            </div>
            <div class="text-sm text-slate-900 dark:text-slate-100">
              {spec.completions ?? 1}
            </div>
          </div>
          <div>
            <div class="text-xs font-medium text-slate-500 dark:text-slate-400">
              Parallelism
            </div>
            <div class="text-sm text-slate-900 dark:text-slate-100">
              {spec.parallelism ?? 1}
            </div>
          </div>
          <div>
            <div class="text-xs font-medium text-slate-500 dark:text-slate-400">
              Backoff Limit
            </div>
            <div class="text-sm text-slate-900 dark:text-slate-100">
              {spec.backoffLimit ?? 6}
            </div>
          </div>
        </div>
      </div>

      {/* Status */}
      <div>
        <h4 class="text-xs font-medium uppercase text-slate-500 dark:text-slate-400 mb-2">
          Status
        </h4>
        <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
          <div>
            <div class="text-xs font-medium text-slate-500 dark:text-slate-400">
              Active
            </div>
            <div class="text-sm text-slate-900 dark:text-slate-100">
              {status.active ?? 0}
            </div>
          </div>
          <div>
            <div class="text-xs font-medium text-slate-500 dark:text-slate-400">
              Succeeded
            </div>
            <div class="text-sm text-slate-900 dark:text-slate-100">
              {status.succeeded ?? 0}
            </div>
          </div>
          <div>
            <div class="text-xs font-medium text-slate-500 dark:text-slate-400">
              Failed
            </div>
            <div class="text-sm text-slate-900 dark:text-slate-100">
              {status.failed ?? 0}
            </div>
          </div>
          <div>
            <div class="text-xs font-medium text-slate-500 dark:text-slate-400">
              Start Time
            </div>
            <div class="text-sm text-slate-900 dark:text-slate-100">
              {status.startTime ? age(status.startTime) + " ago" : "-"}
            </div>
          </div>
          <div>
            <div class="text-xs font-medium text-slate-500 dark:text-slate-400">
              Completion Time
            </div>
            <div class="text-sm text-slate-900 dark:text-slate-100">
              {status.completionTime
                ? age(status.completionTime) + " ago"
                : "-"}
            </div>
          </div>
        </div>
      </div>

      {/* Conditions */}
      {status.conditions && <ConditionsTable conditions={status.conditions} />}
    </div>
  );
}
