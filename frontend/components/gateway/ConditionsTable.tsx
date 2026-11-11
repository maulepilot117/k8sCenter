import type { Condition } from "@/lib/gateway-types.ts";
import { withUniqueKeys } from "@/lib/unique-keys.ts";

function formatTime(ts?: string): string {
  if (!ts) return "-";
  const d = new Date(ts);
  return `${d.toLocaleDateString()} ${d.toLocaleTimeString()}`;
}

export default function ConditionsTable({
  conditions,
}: {
  conditions?: Condition[];
}) {
  if (!conditions || conditions.length === 0) {
    return null;
  }

  return (
    <div className="mt-4">
      <h3 className="text-sm font-medium text-text-secondary mb-2">
        Conditions
      </h3>
      <div className="rounded-lg border border-border-primary overflow-hidden">
        <table className="min-w-full divide-y divide-border-subtle">
          <thead className="bg-surface">
            <tr>
              <th className="px-4 py-2 text-left text-xs font-medium text-text-muted">
                Type
              </th>
              <th className="px-4 py-2 text-left text-xs font-medium text-text-muted">
                Status
              </th>
              <th className="px-4 py-2 text-left text-xs font-medium text-text-muted">
                Reason
              </th>
              <th className="px-4 py-2 text-left text-xs font-medium text-text-muted">
                Message
              </th>
              <th className="px-4 py-2 text-left text-xs font-medium text-text-muted">
                Last Transition
              </th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border-subtle">
            {withUniqueKeys(conditions, (x) => x.type).map(
              ({ item: c, key }) => (
                <tr key={key}>
                  <td className="px-4 py-2 text-sm text-text-primary font-medium">
                    {c.type}
                  </td>
                  <td className="px-4 py-2 text-sm">
                    <span
                      className={
                        c.status === "True"
                          ? "text-success"
                          : c.status === "False"
                            ? "text-danger"
                            : "text-warning"
                      }
                    >
                      {c.status}
                    </span>
                  </td>
                  <td className="px-4 py-2 text-sm text-text-secondary">
                    {c.reason || "-"}
                  </td>
                  <td className="px-4 py-2 text-sm text-text-muted max-w-xs truncate">
                    {c.message || "-"}
                  </td>
                  <td className="px-4 py-2 text-sm text-text-muted">
                    {formatTime(c.lastTransitionTime)}
                  </td>
                </tr>
              ),
            )}
          </tbody>
        </table>
      </div>
    </div>
  );
}
