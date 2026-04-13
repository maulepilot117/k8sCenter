import type { ParentRef } from "@/lib/gateway-types.ts";

export default function ParentGatewaysTable(
  { parentRefs }: { parentRefs?: ParentRef[] },
) {
  if (!parentRefs || parentRefs.length === 0) {
    return null;
  }

  return (
    <div class="mt-4">
      <h3 class="text-sm font-medium text-text-secondary mb-2">
        Parent Gateways
      </h3>
      <div class="rounded-lg border border-border-primary overflow-hidden">
        <table class="min-w-full divide-y divide-border-subtle">
          <thead class="bg-surface">
            <tr>
              <th class="px-4 py-2 text-left text-xs font-medium text-text-muted">
                Name
              </th>
              <th class="px-4 py-2 text-left text-xs font-medium text-text-muted">
                Namespace
              </th>
              <th class="px-4 py-2 text-left text-xs font-medium text-text-muted">
                Section
              </th>
              <th class="px-4 py-2 text-left text-xs font-medium text-text-muted">
                Status
              </th>
            </tr>
          </thead>
          <tbody class="divide-y divide-border-subtle">
            {parentRefs.map((ref, i) => (
              <tr key={i}>
                <td class="px-4 py-2 text-sm">
                  <a
                    href={`/networking/gateway-api/gateways/${ref.namespace}/${ref.name}`}
                    class="text-brand hover:underline"
                  >
                    {ref.name}
                  </a>
                </td>
                <td class="px-4 py-2 text-sm text-text-secondary">
                  {ref.namespace || "-"}
                </td>
                <td class="px-4 py-2 text-sm text-text-muted">
                  {ref.sectionName || "-"}
                </td>
                <td class="px-4 py-2 text-sm">
                  <span
                    class={ref.status === "Accepted"
                      ? "text-success"
                      : ref.status
                      ? "text-danger"
                      : "text-text-muted"}
                  >
                    {ref.status || "-"}
                  </span>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}
