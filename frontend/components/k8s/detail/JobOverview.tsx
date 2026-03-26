import type { Job, K8sResource } from "@/lib/k8s-types.ts";
import { age } from "@/lib/format.ts";
import { Field, SectionHeader } from "@/components/ui/Field.tsx";
import { ConditionsTable } from "./ConditionsTable.tsx";

export function JobOverview({ resource }: { resource: K8sResource }) {
  const j = resource as Job;
  const spec = j.spec;
  const status = j.status;

  return (
    <div class="space-y-4">
      {/* Configuration */}
      <div>
        <SectionHeader>Configuration</SectionHeader>
        <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
          <Field label="Completions" value={String(spec.completions ?? 1)} />
          <Field label="Parallelism" value={String(spec.parallelism ?? 1)} />
          <Field label="Backoff Limit" value={String(spec.backoffLimit ?? 6)} />
        </div>
      </div>

      {/* Status */}
      <div>
        <SectionHeader>Status</SectionHeader>
        <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
          <Field label="Active" value={String(status.active ?? 0)} />
          <Field label="Succeeded" value={String(status.succeeded ?? 0)} />
          <Field label="Failed" value={String(status.failed ?? 0)} />
          <Field
            label="Start Time"
            value={status.startTime ? age(status.startTime) + " ago" : "-"}
          />
          <Field
            label="Completion Time"
            value={status.completionTime
              ? age(status.completionTime) + " ago"
              : "-"}
          />
        </div>
      </div>

      {/* Conditions */}
      {status.conditions && <ConditionsTable conditions={status.conditions} />}
    </div>
  );
}
