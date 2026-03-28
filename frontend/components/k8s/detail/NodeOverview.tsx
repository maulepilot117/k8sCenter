import type { K8sResource, Node } from "@/lib/k8s-types.ts";
import { Field, SectionHeader } from "@/components/ui/Field.tsx";
import { ConditionsTable } from "./ConditionsTable.tsx";

export function NodeOverview({ resource }: { resource: K8sResource }) {
  const n = resource as Node;
  const spec = n.spec;
  const status = n.status;

  const capacity = status?.capacity ?? {};
  const allocatable = status?.allocatable ?? {};
  const capacityKeys = [
    ...new Set([...Object.keys(capacity), ...Object.keys(allocatable)]),
  ].sort();

  return (
    <div class="space-y-4">
      {/* System Info */}
      {status?.nodeInfo && (
        <div>
          <SectionHeader>System Info</SectionHeader>
          <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
            <Field
              label="Kubelet Version"
              value={status.nodeInfo.kubeletVersion ?? "-"}
            />
            <Field label="OS Image" value={status.nodeInfo.osImage ?? "-"} />
            <Field
              label="Architecture"
              value={status.nodeInfo.architecture ?? "-"}
            />
            <Field
              label="Container Runtime"
              value={status.nodeInfo.containerRuntimeVersion ?? "-"}
            />
          </div>
        </div>
      )}

      {/* Addresses */}
      {status?.addresses && status.addresses.length > 0 && (
        <div>
          <SectionHeader>Addresses</SectionHeader>
          <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
            {status.addresses.map((a) => (
              <Field key={a.type} label={a.type} value={a.address} mono />
            ))}
          </div>
        </div>
      )}

      {/* Capacity vs Allocatable */}
      {capacityKeys.length > 0 && (
        <div>
          <SectionHeader>Capacity vs Allocatable</SectionHeader>
          <div class="overflow-x-auto rounded-md border border-border-primary">
            <table class="w-full text-sm">
              <thead>
                <tr class="border-b border-border-primary">
                  <th class="px-3 py-1.5 text-left text-xs font-medium text-text-muted">
                    Resource
                  </th>
                  <th class="px-3 py-1.5 text-left text-xs font-medium text-text-muted">
                    Capacity
                  </th>
                  <th class="px-3 py-1.5 text-left text-xs font-medium text-text-muted">
                    Allocatable
                  </th>
                </tr>
              </thead>
              <tbody class="divide-y divide-border-subtle">
                {capacityKeys.map((key) => (
                  <tr key={key}>
                    <td class="px-3 py-1.5 font-medium text-text-secondary">
                      {key}
                    </td>
                    <td class="px-3 py-1.5 font-mono text-text-secondary">
                      {capacity[key] ?? "-"}
                    </td>
                    <td class="px-3 py-1.5 font-mono text-text-secondary">
                      {allocatable[key] ?? "-"}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {/* Taints */}
      {spec?.taints && spec.taints.length > 0 && (
        <div>
          <SectionHeader>Taints</SectionHeader>
          <div class="overflow-x-auto rounded-md border border-border-primary">
            <table class="w-full text-sm">
              <thead>
                <tr class="border-b border-border-primary">
                  <th class="px-3 py-1.5 text-left text-xs font-medium text-text-muted">
                    Key
                  </th>
                  <th class="px-3 py-1.5 text-left text-xs font-medium text-text-muted">
                    Value
                  </th>
                  <th class="px-3 py-1.5 text-left text-xs font-medium text-text-muted">
                    Effect
                  </th>
                </tr>
              </thead>
              <tbody class="divide-y divide-border-subtle">
                {spec.taints.map((t) => (
                  <tr key={`${t.key}-${t.effect}`}>
                    <td class="px-3 py-1.5 font-mono text-xs text-accent">
                      {t.key}
                    </td>
                    <td class="px-3 py-1.5 text-text-secondary">
                      {t.value ?? "-"}
                    </td>
                    <td class="px-3 py-1.5 text-text-secondary">
                      {t.effect}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {/* Unschedulable */}
      {spec?.unschedulable && (
        <div>
          <span class="inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium ring-1 ring-inset bg-warning-dim text-warning ring-warning">
            Cordoned (Unschedulable)
          </span>
        </div>
      )}

      {/* Conditions */}
      {status?.conditions && <ConditionsTable conditions={status.conditions} />}
    </div>
  );
}
