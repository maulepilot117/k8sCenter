/** @jsxImportSource react */
import { useEffect, useState } from "react";
import { IssuerTypeBadge } from "@/components/ui/CertificateBadges.tsx";
import { Spinner } from "@/components/ui/Spinner.tsx";
import { apiGet } from "@/lib/api.ts";
import type { Issuer } from "@/lib/certmanager-types.ts";
import { IS_BROWSER } from "@/src/lib/is-browser.ts";
import { useIsClient } from "@/src/lib/use-is-client.ts";

export default function IssuersList() {
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [issuers, setIssuers] = useState<Issuer[]>([]);
  const isClient = useIsClient();

  useEffect(() => {
    if (!IS_BROWSER) return;

    Promise.all([
      apiGet<Issuer[]>("/v1/certificates/issuers"),
      apiGet<Issuer[]>("/v1/certificates/clusterissuers"),
    ])
      .then(([nsRes, clusterRes]) => {
        const ns = Array.isArray(nsRes.data) ? nsRes.data : [];
        const cl = Array.isArray(clusterRes.data) ? clusterRes.data : [];
        setIssuers([...ns, ...cl]);
      })
      .catch(() => {
        setError("Failed to load issuers");
      })
      .finally(() => {
        setLoading(false);
      });
  }, []);

  if (!isClient) return null;

  return (
    <div className="p-6">
      <div className="flex items-start justify-between mb-1">
        <h1 className="text-2xl font-bold text-text-primary">Issuers</h1>
        <div className="flex gap-2">
          <a
            href="/security/certificates/issuers/new"
            className="inline-flex items-center gap-2 rounded-md border border-border-primary px-3 py-1.5 text-sm font-medium text-text-primary hover:bg-hover"
          >
            Create Issuer
          </a>
          <a
            href="/security/certificates/cluster-issuers/new"
            className="inline-flex items-center gap-2 rounded-md bg-brand px-3 py-1.5 text-sm font-medium hover:bg-brand/90"
            style={{ color: "var(--bg-base)" }}
          >
            Create ClusterIssuer
          </a>
        </div>
      </div>
      <p className="text-sm text-text-muted mb-6">
        cert-manager Issuers and ClusterIssuers.
      </p>

      {loading && (
        <div className="flex justify-center py-12">
          <Spinner className="text-brand" />
        </div>
      )}

      {error && <p className="text-sm text-danger py-4">{error}</p>}

      {!loading && !error && issuers.length > 0 && (
        <div className="overflow-x-auto rounded-lg border border-border-primary">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-border-primary bg-surface">
                <th className="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Name
                </th>
                <th className="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Scope
                </th>
                <th className="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Namespace
                </th>
                <th className="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Type
                </th>
                <th className="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Ready
                </th>
                <th className="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Details
                </th>
              </tr>
            </thead>
            <tbody className="divide-y divide-border-subtle">
              {issuers.map((iss) => (
                <tr key={iss.uid} className="hover:bg-hover/30">
                  <td className="px-3 py-2 font-medium text-text-primary">
                    {iss.name}
                  </td>
                  <td className="px-3 py-2 text-text-secondary">{iss.scope}</td>
                  <td className="px-3 py-2 text-text-secondary">
                    {iss.namespace || "\u2014"}
                  </td>
                  <td className="px-3 py-2">
                    <IssuerTypeBadge type={iss.type} />
                  </td>
                  <td className="px-3 py-2">
                    {iss.ready ? (
                      <span className="text-success text-xs font-medium">
                        Ready
                      </span>
                    ) : (
                      <span className="text-danger text-xs font-medium">
                        Not Ready
                      </span>
                    )}
                  </td>
                  <td className="px-3 py-2 text-text-secondary text-xs truncate max-w-[280px]">
                    {iss.acmeServer || iss.reason || "\u2014"}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {!loading && !error && issuers.length === 0 && (
        <div className="text-center py-12 rounded-lg border border-border-primary bg-elevated">
          <p className="text-text-muted">
            No cert-manager Issuers or ClusterIssuers found. External issuers
            (such as step-issuer's StepClusterIssuer) are not listed here.
          </p>
        </div>
      )}
    </div>
  );
}
