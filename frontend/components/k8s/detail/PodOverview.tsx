import type { K8sResource, Pod } from "@/lib/k8s-types.ts";
import { statusColor } from "@/lib/status-colors.ts";
import { Field, SectionHeader } from "@/components/ui/Field.tsx";
import { ConditionsTable } from "./ConditionsTable.tsx";
import { ContainerResourcesTable } from "./ContainerResourcesTable.tsx";

function containerStateLabel(state: Record<string, unknown>): string {
  if (state.running) return "Running";
  if (state.waiting) {
    const w = state.waiting as Record<string, unknown>;
    return w.reason ? `Waiting: ${w.reason}` : "Waiting";
  }
  if (state.terminated) {
    const t = state.terminated as Record<string, unknown>;
    return t.reason ? `Terminated: ${t.reason}` : "Terminated";
  }
  return "Unknown";
}

function containerStateVariant(state: Record<string, unknown>): string {
  if (state.running) return "running";
  if (state.waiting) return "waiting";
  if (state.terminated) {
    const t = state.terminated as Record<string, unknown>;
    return (t.exitCode as number) === 0 ? "succeeded" : "failed";
  }
  return "unknown";
}

export function PodOverview({ resource }: { resource: K8sResource }) {
  const p = resource as Pod;
  const spec = p.spec;
  const status = p.status;

  return (
    <div class="space-y-4">
      {/* Summary */}
      <div>
        <SectionHeader>Summary</SectionHeader>
        <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
          <div>
            <dt class="text-xs font-medium text-slate-500 dark:text-slate-400">
              Phase
            </dt>
            <dd class="mt-0.5">
              <span
                class={`inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium ring-1 ring-inset ${
                  statusColor(status?.phase)
                }`}
              >
                {status?.phase}
              </span>
            </dd>
          </div>
          <Field label="Node" value={spec.nodeName ?? "-"} />
          <Field
            label="Restart Policy"
            value={spec.restartPolicy ?? "Always"}
          />
        </div>
      </div>

      {/* Container Statuses */}
      {(status?.containerStatuses ?? spec.containers) && (
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
                  <th class="px-3 py-1.5 text-left text-xs font-medium text-slate-500">
                    State
                  </th>
                  <th class="px-3 py-1.5 text-left text-xs font-medium text-slate-500">
                    Ready
                  </th>
                  <th class="px-3 py-1.5 text-left text-xs font-medium text-slate-500">
                    Restarts
                  </th>
                </tr>
              </thead>
              <tbody class="divide-y divide-slate-100 dark:divide-slate-700/50">
                {spec.containers.map((c) => {
                  const cs = status?.containerStatuses?.find((s) =>
                    s.name === c.name
                  );
                  const stateLabel = cs ? containerStateLabel(cs.state) : "-";
                  const stateVar = cs
                    ? containerStateVariant(cs.state)
                    : "unknown";
                  return (
                    <tr key={c.name}>
                      <td class="px-3 py-1.5 font-medium text-slate-700 dark:text-slate-300">
                        {c.name}
                      </td>
                      <td class="px-3 py-1.5 font-mono text-xs text-slate-600 dark:text-slate-400 break-all">
                        {c.image}
                      </td>
                      <td class="px-3 py-1.5">
                        <span
                          class={`inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium ring-1 ring-inset ${
                            statusColor(stateVar)
                          }`}
                        >
                          {stateLabel}
                        </span>
                      </td>
                      <td class="px-3 py-1.5 text-slate-700 dark:text-slate-300">
                        {cs ? (cs.ready ? "Yes" : "No") : "-"}
                      </td>
                      <td class="px-3 py-1.5 text-slate-700 dark:text-slate-300">
                        {cs?.restartCount ?? 0}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {/* Resource Requests & Limits */}
      <ContainerResourcesTable containers={spec.containers ?? []} />

      {/* Conditions */}
      {status?.conditions && <ConditionsTable conditions={status.conditions} />}
    </div>
  );
}
