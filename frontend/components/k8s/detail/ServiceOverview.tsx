import type { K8sResource, Service } from "@/lib/k8s-types.ts";
import { Field, SectionHeader } from "@/components/ui/Field.tsx";
import { KeyValueTable } from "./KeyValueTable.tsx";
import { MeshGoldenSignals } from "@/components/mesh/GoldenSignals.tsx";

export function ServiceOverview({ resource }: { resource: K8sResource }) {
  const s = resource as Service;
  const spec = s.spec;
  const meta = s.metadata;

  return (
    <div class="space-y-4">
      {/* Summary */}
      <div>
        <SectionHeader>Summary</SectionHeader>
        <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
          <Field label="Type" value={spec.type} />
          <Field label="Cluster IP" value={spec.clusterIP ?? "None"} mono />
        </div>
      </div>

      {/* Ports */}
      {spec.ports && spec.ports.length > 0 && (
        <div>
          <SectionHeader>Ports</SectionHeader>
          <div class="overflow-x-auto rounded-md border border-border-primary">
            <table class="w-full text-sm">
              <thead>
                <tr class="border-b border-border-primary">
                  <th class="px-3 py-1.5 text-left text-xs font-medium text-text-muted">
                    Name
                  </th>
                  <th class="px-3 py-1.5 text-left text-xs font-medium text-text-muted">
                    Protocol
                  </th>
                  <th class="px-3 py-1.5 text-left text-xs font-medium text-text-muted">
                    Port
                  </th>
                  <th class="px-3 py-1.5 text-left text-xs font-medium text-text-muted">
                    Target Port
                  </th>
                </tr>
              </thead>
              <tbody class="divide-y divide-border-subtle">
                {spec.ports.map((p, i) => (
                  <tr key={p.name ?? i}>
                    <td class="px-3 py-1.5 text-text-secondary">
                      {p.name ?? "-"}
                    </td>
                    <td class="px-3 py-1.5 text-text-secondary">
                      {p.protocol ?? "TCP"}
                    </td>
                    <td class="px-3 py-1.5 font-mono text-text-secondary">
                      {p.port}
                    </td>
                    <td class="px-3 py-1.5 font-mono text-text-secondary">
                      {p.targetPort != null ? String(p.targetPort) : "-"}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {/* Selector */}
      {spec.selector && Object.keys(spec.selector).length > 0 && (
        <KeyValueTable title="Selector" data={spec.selector} />
      )}

      {/* Service mesh golden signals — silently absent for unmeshed
          services, otherwise refreshes on a 30s cadence. */}
      {meta?.namespace && meta?.name && (
        <MeshGoldenSignals
          namespace={meta.namespace}
          service={meta.name}
        />
      )}
    </div>
  );
}
