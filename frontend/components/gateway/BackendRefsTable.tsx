import type { BackendRef } from "@/lib/gateway-types.ts";
import { withUniqueKeys } from "@/lib/unique-keys.ts";

export default function BackendRefsTable({
  backendRefs,
}: {
  backendRefs?: BackendRef[];
}) {
  if (!backendRefs || backendRefs.length === 0) {
    return null;
  }

  return (
    <div className="mt-4">
      <h3 className="text-sm font-medium text-text-secondary mb-2">
        Backend References
      </h3>
      <div className="rounded-lg border border-border-primary overflow-hidden">
        <table className="min-w-full divide-y divide-border-subtle">
          <thead className="bg-surface">
            <tr>
              <th className="px-4 py-2 text-left text-xs font-medium text-text-muted">
                Kind
              </th>
              <th className="px-4 py-2 text-left text-xs font-medium text-text-muted">
                Name
              </th>
              <th className="px-4 py-2 text-left text-xs font-medium text-text-muted">
                Namespace
              </th>
              <th className="px-4 py-2 text-left text-xs font-medium text-text-muted">
                Port
              </th>
              <th className="px-4 py-2 text-left text-xs font-medium text-text-muted">
                Weight
              </th>
              <th className="px-4 py-2 text-left text-xs font-medium text-text-muted">
                Resolved
              </th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border-subtle">
            {withUniqueKeys(backendRefs, (r) =>
              [r.kind, r.namespace, r.name, r.port, r.weight].join("/"),
            ).map(({ item: ref, key }) => (
              <tr key={key}>
                <td className="px-4 py-2 text-sm text-text-muted">
                  {ref.kind}
                </td>
                <td className="px-4 py-2 text-sm">
                  {ref.resolved && ref.kind === "Service" ? (
                    <a
                      href={`/networking/services/${ref.namespace}/${ref.name}`}
                      className="text-brand hover:underline"
                    >
                      {ref.name}
                    </a>
                  ) : (
                    <span className="text-text-primary">{ref.name}</span>
                  )}
                </td>
                <td className="px-4 py-2 text-sm text-text-secondary">
                  {ref.namespace || "-"}
                </td>
                <td className="px-4 py-2 text-sm text-text-muted">
                  {ref.port != null ? ref.port : "-"}
                </td>
                <td className="px-4 py-2 text-sm text-text-muted">
                  {ref.weight != null ? ref.weight : "-"}
                </td>
                <td className="px-4 py-2 text-sm">
                  <span
                    className={ref.resolved ? "text-success" : "text-danger"}
                  >
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
