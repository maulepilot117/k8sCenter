import type { K8sResource } from "@/lib/k8s-types.ts";
import type { CronJob } from "@/lib/k8s-types.ts";
import { statusColor } from "@/lib/status-colors.ts";
import { age } from "@/lib/format.ts";

export function CronJobOverview({ resource }: { resource: K8sResource }) {
  const cj = resource as CronJob;
  const spec = cj.spec;
  const status = cj.status;

  // Extract concurrency policy from spec
  const concurrencyPolicy = (spec as Record<string, unknown>)
    .concurrencyPolicy as string | undefined;

  const suspended = spec.suspend ?? false;
  const activeJobs = status?.active?.length ?? 0;

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
              Schedule
            </div>
            <div class="text-sm font-mono text-slate-900 dark:text-slate-100">
              {spec.schedule}
            </div>
          </div>
          <div>
            <div class="text-xs font-medium text-slate-500 dark:text-slate-400">
              Suspend
            </div>
            <div class="mt-0.5">
              <span
                class={`inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium ring-1 ring-inset ${
                  suspended ? statusColor("warning") : statusColor("active")
                }`}
              >
                {suspended ? "Yes" : "No"}
              </span>
            </div>
          </div>
          <div>
            <div class="text-xs font-medium text-slate-500 dark:text-slate-400">
              Concurrency Policy
            </div>
            <div class="text-sm text-slate-900 dark:text-slate-100">
              {concurrencyPolicy ?? "Allow"}
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
              Last Schedule
            </div>
            <div class="text-sm text-slate-900 dark:text-slate-100">
              {status?.lastScheduleTime
                ? age(status.lastScheduleTime) + " ago"
                : "-"}
            </div>
          </div>
          <div>
            <div class="text-xs font-medium text-slate-500 dark:text-slate-400">
              Active Jobs
            </div>
            <div class="text-sm text-slate-900 dark:text-slate-100">
              {activeJobs}
            </div>
          </div>
        </div>
      </div>

      {/* Active Job Names */}
      {status?.active && status.active.length > 0 && (
        <div>
          <h4 class="text-xs font-medium uppercase text-slate-500 dark:text-slate-400 mb-2">
            Active Job References
          </h4>
          <div class="flex flex-wrap gap-1.5">
            {status.active.map((ref) => (
              <span
                key={ref.name}
                class="inline-flex items-center rounded-full px-2 py-0.5 text-xs font-mono font-medium ring-1 ring-inset bg-blue-50 text-blue-700 ring-blue-600/20 dark:bg-blue-500/10 dark:text-blue-400 dark:ring-blue-500/20"
              >
                {ref.name}
              </span>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}
