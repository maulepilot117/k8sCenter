import type { K8sResource } from "@/lib/k8s-types.ts";
import { age } from "@/lib/format.ts";
import { KeyValueTable } from "./KeyValueTable.tsx";

interface MetadataSectionProps {
  resource: K8sResource;
}

export function MetadataSection({ resource }: MetadataSectionProps) {
  const meta = resource.metadata;

  return (
    <div class="space-y-4">
      <h3 class="text-sm font-semibold text-slate-900 dark:text-white">
        Metadata
      </h3>
      <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
        <Field label="Name" value={meta.name} />
        {meta.namespace && <Field label="Namespace" value={meta.namespace} />}
        <Field label="UID" value={meta.uid} mono />
        <Field
          label="Created"
          value={`${meta.creationTimestamp} (${age(meta.creationTimestamp)})`}
        />
        {meta.resourceVersion && (
          <Field label="Resource Version" value={meta.resourceVersion} />
        )}
        {meta.deletionTimestamp && (
          <Field label="Deletion Timestamp" value={meta.deletionTimestamp} />
        )}
      </div>

      {meta.ownerReferences && meta.ownerReferences.length > 0 && (
        <div>
          <h4 class="text-xs font-medium uppercase text-slate-500 dark:text-slate-400 mb-2">
            Owner References
          </h4>
          <div class="space-y-1">
            {meta.ownerReferences.map((ref) => (
              <div
                key={ref.uid}
                class="text-sm text-slate-700 dark:text-slate-300"
              >
                {ref.kind}/{ref.name}
                {ref.controller && (
                  <span class="ml-2 text-xs text-slate-400">(controller)</span>
                )}
              </div>
            ))}
          </div>
        </div>
      )}

      {meta.finalizers && meta.finalizers.length > 0 && (
        <div>
          <h4 class="text-xs font-medium uppercase text-slate-500 dark:text-slate-400 mb-2">
            Finalizers
          </h4>
          <div class="space-y-1">
            {meta.finalizers.map((f) => (
              <div
                key={f}
                class="text-sm font-mono text-slate-700 dark:text-slate-300"
              >
                {f}
              </div>
            ))}
          </div>
        </div>
      )}

      {meta.labels && Object.keys(meta.labels).length > 0 && (
        <KeyValueTable title="Labels" data={meta.labels} />
      )}
      {meta.annotations && Object.keys(meta.annotations).length > 0 && (
        <KeyValueTable title="Annotations" data={meta.annotations} />
      )}
    </div>
  );
}

function Field({
  label,
  value,
  mono,
}: {
  label: string;
  value: string;
  mono?: boolean;
}) {
  return (
    <div>
      <dt class="text-xs font-medium text-slate-500 dark:text-slate-400">
        {label}
      </dt>
      <dd
        class={`mt-0.5 text-sm text-slate-900 dark:text-slate-100 break-all ${
          mono ? "font-mono text-xs" : ""
        }`}
      >
        {value}
      </dd>
    </div>
  );
}
