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
export function ContainerResourcesTable(
  { containers }: ContainerResourcesTableProps,
) {
  // Only show if at least one container has resource specs
  const hasResources = containers.some((c) =>
    c.resources?.requests || c.resources?.limits
  );
  if (!hasResources) return null;

  return (
    <div>
      <SectionHeader>Resource Requests &amp; Limits</SectionHeader>
      <div class="overflow-x-auto rounded-md border border-border-primary">
        <table class="w-full text-sm">
          <thead>
            <tr class="border-b border-border-primary">
              <th class="px-3 py-1.5 text-left text-xs font-medium text-text-muted">
                Container
              </th>
              <th class="px-3 py-1.5 text-left text-xs font-medium text-text-muted">
                CPU Request
              </th>
              <th class="px-3 py-1.5 text-left text-xs font-medium text-text-muted">
                CPU Limit
              </th>
              <th class="px-3 py-1.5 text-left text-xs font-medium text-text-muted">
                Memory Request
              </th>
              <th class="px-3 py-1.5 text-left text-xs font-medium text-text-muted">
                Memory Limit
              </th>
            </tr>
          </thead>
          <tbody class="divide-y divide-border-subtle">
            {containers.map((c) => (
              <tr key={c.name}>
                <td class="px-3 py-1.5 font-medium text-text-secondary">
                  {c.name}
                </td>
                <td class="px-3 py-1.5 font-mono text-xs text-text-secondary">
                  {c.resources?.requests?.cpu ?? "-"}
                </td>
                <td class="px-3 py-1.5 font-mono text-xs text-text-secondary">
                  {c.resources?.limits?.cpu ?? "-"}
                </td>
                <td class="px-3 py-1.5 font-mono text-xs text-text-secondary">
                  {c.resources?.requests?.memory ?? "-"}
                </td>
                <td class="px-3 py-1.5 font-mono text-xs text-text-secondary">
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
