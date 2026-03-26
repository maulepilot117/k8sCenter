import type { CronJob, K8sResource } from"@/lib/k8s-types.ts";
import { statusColor } from"@/lib/status-colors.ts";
import { age } from"@/lib/format.ts";
import { Field, SectionHeader } from"@/components/ui/Field.tsx";

export function CronJobOverview({ resource }: { resource: K8sResource }) {
 const cj = resource as CronJob;
 const spec = cj.spec;
 const status = cj.status;

 const suspended = spec.suspend ?? false;
 const activeJobs = status?.active?.length ?? 0;

 return (
 <div class="space-y-4">
 {/* Configuration */}
 <div>
 <SectionHeader>Configuration</SectionHeader>
 <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
 <Field label="Schedule" value={spec.schedule} mono />
 <div>
 <dt class="text-xs font-medium text-text-muted">
 Suspend
 </dt>
 <dd class="mt-0.5">
 <span
 class={`inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium ring-1 ring-inset ${
 suspended ? statusColor("warning") : statusColor("active")
 }`}
 >
 {suspended ?"Yes" :"No"}
 </span>
 </dd>
 </div>
 <Field
 label="Concurrency Policy"
 value={spec.concurrencyPolicy ??"Allow"}
 />
 </div>
 </div>

 {/* Status */}
 <div>
 <SectionHeader>Status</SectionHeader>
 <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
 <Field
 label="Last Schedule"
 value={status?.lastScheduleTime
 ? age(status.lastScheduleTime) +" ago"
 :"-"}
 />
 <Field label="Active Jobs" value={String(activeJobs)} />
 </div>
 </div>

 {/* Active Job Names */}
 {status?.active && status.active.length > 0 && (
 <div>
 <SectionHeader>Active Job References</SectionHeader>
 <div class="flex flex-wrap gap-1.5">
 {status.active.map((ref) => (
 <span
 key={ref.name}
 class="inline-flex items-center rounded-full px-2 py-0.5 text-xs font-mono font-medium ring-1 ring-inset bg-accent-dim text-blue-700 ring-blue-600/20 bg-accent-dim text-accent ring-accent"
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
