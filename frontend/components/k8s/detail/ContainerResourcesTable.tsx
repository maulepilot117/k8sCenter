import { SectionHeader } from "@/components/ui/Field.tsx";

interface Container {
  name: string;
  resources?: {
    requests?: Record<string, string>;
    limits?: Record<string, string>;
  };
}

interface ContainerResourcesTableProps {
  containers: Container[];
}

/**
 * Shows resource requests and limits per container.
 * Renders nothing if no containers have resource specifications.
 */
export function ContainerResourcesTable({
  containers,
}: ContainerResourcesTableProps) {
  // Only show if at least one container has resource specs
  const hasResources = containers.some(
    (c) => c.resources?.requests || c.resources?.limits,
  );
  if (!hasResources) return null;

  return (
    <div>
      <SectionHeader>Resource Requests &amp; Limits</SectionHeader>
      <div className="overflow-x-auto rounded-md border border-border-primary">
        <table className="w-full text-sm">
          <thead>
            <tr className="border-b border-border-primary">
              <th className="px-3 py-1.5 text-left text-xs font-medium text-text-muted">
                Container
              </th>
              <th className="px-3 py-1.5 text-left text-xs font-medium text-text-muted">
                CPU Request
              </th>
              <th className="px-3 py-1.5 text-left text-xs font-medium text-text-muted">
                CPU Limit
              </th>
              <th className="px-3 py-1.5 text-left text-xs font-medium text-text-muted">
                Memory Request
              </th>
              <th className="px-3 py-1.5 text-left text-xs font-medium text-text-muted">
                Memory Limit
              </th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border-subtle">
            {containers.map((c) => (
              <tr key={c.name}>
                <td className="px-3 py-1.5 font-medium text-text-secondary">
                  {c.name}
                </td>
                <td className="px-3 py-1.5 font-mono text-xs text-text-secondary">
                  {c.resources?.requests?.cpu ?? "-"}
                </td>
                <td className="px-3 py-1.5 font-mono text-xs text-text-secondary">
                  {c.resources?.limits?.cpu ?? "-"}
                </td>
                <td className="px-3 py-1.5 font-mono text-xs text-text-secondary">
                  {c.resources?.requests?.memory ?? "-"}
                </td>
                <td className="px-3 py-1.5 font-mono text-xs text-text-secondary">
                  {c.resources?.limits?.memory ?? "-"}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}
