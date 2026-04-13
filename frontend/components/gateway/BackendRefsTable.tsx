import type { BackendRef } from "@/lib/gateway-types.ts";

export default function BackendRefsTable(
  { backendRefs }: { backendRefs?: BackendRef[] },
) {
  if (!backendRefs || backendRefs.length === 0) {
    return null;
  }

  return (
    <div class="mt-4">
      <h3 class="text-sm font-medium text-text-secondary mb-2">
        Backend References
      </h3>
      <div class="rounded-lg border border-border-primary overflow-hidden">
        <table class="min-w-full divide-y divide-border-subtle">
          <thead class="bg-surface">
            <tr>
              <th class="px-4 py-2 text-left text-xs font-medium text-text-muted">
                Kind
              </th>
              <th class="px-4 py-2 text-left text-xs font-medium text-text-muted">
                Name
              </th>
              <th class="px-4 py-2 text-left text-xs font-medium text-text-muted">
                Namespace
              </th>
              <th class="px-4 py-2 text-left text-xs font-medium text-text-muted">
                Port
              </th>
              <th class="px-4 py-2 text-left text-xs font-medium text-text-muted">
                Weight
              </th>
              <th class="px-4 py-2 text-left text-xs font-medium text-text-muted">
                Resolved
              </th>
            </tr>
          </thead>
          <tbody class="divide-y divide-border-subtle">
            {backendRefs.map((ref, i) => (
              <tr key={i}>
                <td class="px-4 py-2 text-sm text-text-muted">{ref.kind}</td>
                <td class="px-4 py-2 text-sm">
                  {ref.resolved && ref.kind === "Service"
                    ? (
                      <a
                        href={`/networking/services/${ref.namespace}/${ref.name}`}
                        class="text-brand hover:underline"
                      >
                        {ref.name}
                      </a>
                    )
                    : <span class="text-text-primary">{ref.name}</span>}
                </td>
                <td class="px-4 py-2 text-sm text-text-secondary">
                  {ref.namespace || "-"}
                </td>
                <td class="px-4 py-2 text-sm text-text-muted">
                  {ref.port != null ? ref.port : "-"}
                </td>
                <td class="px-4 py-2 text-sm text-text-muted">
                  {ref.weight != null ? ref.weight : "-"}
                </td>
                <td class="px-4 py-2 text-sm">
                  <span class={ref.resolved ? "text-success" : "text-danger"}>
                    {ref.resolved ? "Yes" : "No"}
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
