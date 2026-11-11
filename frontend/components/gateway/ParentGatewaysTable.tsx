import type { ParentRef } from "@/lib/gateway-types.ts";
import { withUniqueKeys } from "@/lib/unique-keys.ts";

export default function ParentGatewaysTable({
  parentRefs,
}: {
  parentRefs?: ParentRef[];
}) {
  if (!parentRefs || parentRefs.length === 0) {
    return null;
  }

  return (
    <div className="mt-4">
      <h3 className="text-sm font-medium text-text-secondary mb-2">
        Parent Gateways
      </h3>
      <div className="rounded-lg border border-border-primary overflow-hidden">
        <table className="min-w-full divide-y divide-border-subtle">
          <thead className="bg-surface">
            <tr>
              <th className="px-4 py-2 text-left text-xs font-medium text-text-muted">
                Name
              </th>
              <th className="px-4 py-2 text-left text-xs font-medium text-text-muted">
                Namespace
              </th>
              <th className="px-4 py-2 text-left text-xs font-medium text-text-muted">
                Section
              </th>
              <th className="px-4 py-2 text-left text-xs font-medium text-text-muted">
                Status
              </th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border-subtle">
            {withUniqueKeys(parentRefs, (r) =>
              [r.kind, r.namespace, r.name, r.sectionName].join("/"),
            ).map(({ item: ref, key }) => (
              <tr key={key}>
                <td className="px-4 py-2 text-sm">
                  {/* Only a Gateway parent has a Gateway detail page; a
                      parentRef can also name another kind (a Service). */}
                  {ref.kind === "Gateway" && ref.namespace ? (
                    <a
                      href={`/networking/gateway-api/gateways/${ref.namespace}/${ref.name}`}
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
                  {ref.sectionName || "-"}
                </td>
                <td className="px-4 py-2 text-sm">
                  <span
                    className={
                      ref.status === "Accepted"
                        ? "text-success"
                        : ref.status
                          ? "text-danger"
                          : "text-text-muted"
                    }
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
