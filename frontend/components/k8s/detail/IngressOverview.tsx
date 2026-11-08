/** @jsxImportSource react */

import { Field, SectionHeader } from "@/components/ui/Field.tsx";
import type { Ingress, K8sResource } from "@/lib/k8s-types.ts";
import { withUniqueKeys } from "@/lib/unique-keys.ts";

function formatBackend(backend: unknown): string {
  const b = backend as Record<string, unknown>;
  if (b.service) {
    const svc = b.service as {
      name?: string;
      port?: { number?: number; name?: string };
    };
    const port = svc.port?.number ?? svc.port?.name ?? "";
    return `${svc.name ?? "?"}:${port}`;
  }
  return JSON.stringify(backend);
}

export function IngressOverview({ resource }: { resource: K8sResource }) {
  const ing = resource as Ingress;
  const spec = ing.spec;
  const lbIngress = ing.status?.loadBalancer?.ingress;

  // Flatten rules into rows
  const rows: Array<{
    id: number;
    host: string;
    path: string;
    backend: string;
  }> = [];
  for (const rule of spec.rules ?? []) {
    const host = rule.host ?? "*";
    for (const p of rule.http?.paths ?? []) {
      rows.push({
        id: rows.length,
        host,
        path: p.path ?? "/",
        backend: formatBackend(p.backend),
      });
    }
  }

  return (
    <div className="space-y-4">
      {/* Load Balancer */}
      {lbIngress && lbIngress.length > 0 && (
        <div>
          <SectionHeader>Load Balancer</SectionHeader>
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
            {withUniqueKeys(lbIngress, (lb) => lb.ip ?? lb.hostname ?? "").map(
              ({ item: lb, key }) => (
                <Field
                  key={key}
                  label={lb.ip ? "IP" : "Hostname"}
                  value={lb.ip ?? lb.hostname ?? "-"}
                  mono
                />
              ),
            )}
          </div>
        </div>
      )}

      {/* Rules */}
      {rows.length > 0 && (
        <div>
          <SectionHeader>Rules</SectionHeader>
          <div className="overflow-x-auto rounded-md border border-border-primary">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-border-primary">
                  <th className="px-3 py-1.5 text-left text-xs font-medium text-text-muted">
                    Host
                  </th>
                  <th className="px-3 py-1.5 text-left text-xs font-medium text-text-muted">
                    Path
                  </th>
                  <th className="px-3 py-1.5 text-left text-xs font-medium text-text-muted">
                    Backend
                  </th>
                </tr>
              </thead>
              <tbody className="divide-y divide-border-subtle">
                {rows.map((r) => (
                  <tr key={r.id}>
                    <td className="px-3 py-1.5 font-mono text-text-secondary">
                      {r.host}
                    </td>
                    <td className="px-3 py-1.5 font-mono text-text-secondary">
                      {r.path}
                    </td>
                    <td className="px-3 py-1.5 font-mono text-text-secondary">
                      {r.backend}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {/* TLS */}
      {spec.tls && spec.tls.length > 0 && (
        <div>
          <SectionHeader>TLS</SectionHeader>
          <div className="overflow-x-auto rounded-md border border-border-primary">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-border-primary">
                  <th className="px-3 py-1.5 text-left text-xs font-medium text-text-muted">
                    Hosts
                  </th>
                  <th className="px-3 py-1.5 text-left text-xs font-medium text-text-muted">
                    Secret Name
                  </th>
                </tr>
              </thead>
              <tbody className="divide-y divide-border-subtle">
                {withUniqueKeys(
                  spec.tls,
                  (t) => `${t.hosts?.join(",")}|${t.secretName}`,
                ).map(({ item: t, key }) => (
                  <tr key={key}>
                    <td className="px-3 py-1.5 font-mono text-text-secondary">
                      {t.hosts?.join(",") ?? "*"}
                    </td>
                    <td className="px-3 py-1.5 text-text-secondary">
                      {t.secretName ?? "-"}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}
    </div>
  );
}
