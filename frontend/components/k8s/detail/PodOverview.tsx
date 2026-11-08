/** @jsxImportSource react */
import { Field, SectionHeader } from "@/components/ui/Field.tsx";
import type { K8sResource, Pod } from "@/lib/k8s-types.ts";
import { statusColor } from "@/lib/status-colors.ts";
import { ConditionsTable } from "./ConditionsTable.tsx";
import { ContainerResourcesTable } from "./ContainerResourcesTable.tsx";
import { VulnerabilityLink } from "./VulnerabilityLink.tsx";

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
    <div className="space-y-4">
      <VulnerabilityLink
        namespace={p.metadata.namespace}
        kind="Pod"
        name={p.metadata.name}
      />

      {/* Summary */}
      <div>
        <SectionHeader>Summary</SectionHeader>
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
          <div>
            <dt className="text-xs font-medium text-text-muted">Phase</dt>
            <dd className="mt-0.5">
              <span
                className={`inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium ring-1 ring-inset ${statusColor(
                  status?.phase,
                )}`}
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
          <div className="overflow-x-auto rounded-md border border-border-primary">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-border-primary">
                  <th className="px-3 py-1.5 text-left text-xs font-medium text-text-muted">
                    Name
                  </th>
                  <th className="px-3 py-1.5 text-left text-xs font-medium text-text-muted">
                    Image
                  </th>
                  <th className="px-3 py-1.5 text-left text-xs font-medium text-text-muted">
                    State
                  </th>
                  <th className="px-3 py-1.5 text-left text-xs font-medium text-text-muted">
                    Ready
                  </th>
                  <th className="px-3 py-1.5 text-left text-xs font-medium text-text-muted">
                    Restarts
                  </th>
                </tr>
              </thead>
              <tbody className="divide-y divide-border-subtle">
                {spec.containers.map((c) => {
                  const cs = status?.containerStatuses?.find(
                    (s) => s.name === c.name,
                  );
                  const stateLabel = cs ? containerStateLabel(cs.state) : "-";
                  const stateVar = cs
                    ? containerStateVariant(cs.state)
                    : "unknown";
                  return (
                    <tr key={c.name}>
                      <td className="px-3 py-1.5 font-medium text-text-secondary">
                        {c.name}
                      </td>
                      <td className="px-3 py-1.5 font-mono text-xs text-text-secondary break-all">
                        {c.image}
                      </td>
                      <td className="px-3 py-1.5">
                        <span
                          className={`inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium ring-1 ring-inset ${statusColor(
                            stateVar,
                          )}`}
                        >
                          {stateLabel}
                        </span>
                      </td>
                      <td className="px-3 py-1.5 text-text-secondary">
                        {cs ? (cs.ready ? "Yes" : "No") : "-"}
                      </td>
                      <td className="px-3 py-1.5 text-text-secondary">
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
