import type { DaemonSet, K8sResource } from "@/lib/k8s-types.ts";
import { Field, SectionHeader } from "@/components/ui/Field.tsx";
import { ContainerResourcesTable } from "./ContainerResourcesTable.tsx";
import { KeyValueTable } from "./KeyValueTable.tsx";

export function DaemonSetOverview({ resource }: { resource: K8sResource }) {
  const d = resource as DaemonSet;
  const spec = d.spec;
  const status = d.status;
  const containers = spec.template?.spec?.containers ?? [];

  return (
    <div class="space-y-4">
      {/* Counts */}
      <div>
        <SectionHeader>Status</SectionHeader>
        <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
          <Field
            label="Desired"
            value={String(status?.desiredNumberScheduled ?? 0)}
          />
          <Field
            label="Current"
            value={String(status?.currentNumberScheduled ?? 0)}
          />
          <Field label="Ready" value={String(status?.numberReady ?? 0)} />
          <Field
            label="Available"
            value={String(status?.numberAvailable ?? 0)}
          />
        </div>
      </div>

      {/* Container Images */}
      {containers.length > 0 && (
        <div>
          <SectionHeader>Containers</SectionHeader>
          <div class="overflow-x-auto rounded-md border border-slate-200 dark:border-slate-700">
            <table class="w-full text-sm">
              <thead>
                <tr class="border-b border-slate-200 dark:border-slate-700">
                  <th class="px-3 py-1.5 text-left text-xs font-medium text-slate-500">
                    Name
                  </th>
                  <th class="px-3 py-1.5 text-left text-xs font-medium text-slate-500">
                    Image
                  </th>
                </tr>
              </thead>
              <tbody class="divide-y divide-slate-100 dark:divide-slate-700/50">
                {containers.map((c) => (
                  <tr key={c.name}>
                    <td class="px-3 py-1.5 font-medium text-slate-700 dark:text-slate-300">
                      {c.name}
                    </td>
                    <td class="px-3 py-1.5 font-mono text-xs text-slate-600 dark:text-slate-400 break-all">
                      {c.image}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {/* Resource Requests & Limits */}
      <ContainerResourcesTable containers={containers} />

      {/* Selector */}
      {spec.selector?.matchLabels && (
        <KeyValueTable title="Selector" data={spec.selector.matchLabels} />
      )}
    </div>
  );
}
