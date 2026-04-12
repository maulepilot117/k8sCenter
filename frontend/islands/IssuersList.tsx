import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { useEffect } from "preact/hooks";
import { apiGet } from "@/lib/api.ts";
import { Spinner } from "@/components/ui/Spinner.tsx";
import { IssuerTypeBadge } from "@/components/ui/CertificateBadges.tsx";
import type { Issuer } from "@/lib/certmanager-types.ts";

export default function IssuersList() {
  const loading = useSignal(true);
  const error = useSignal<string | null>(null);
  const issuers = useSignal<Issuer[]>([]);

  useEffect(() => {
    if (!IS_BROWSER) return;

    Promise.all([
      apiGet<Issuer[]>("/v1/certificates/issuers"),
      apiGet<Issuer[]>("/v1/certificates/clusterissuers"),
    ])
      .then(([nsRes, clusterRes]) => {
        const ns = Array.isArray(nsRes.data) ? nsRes.data : [];
        const cl = Array.isArray(clusterRes.data) ? clusterRes.data : [];
        issuers.value = [...ns, ...cl];
      })
      .catch(() => {
        error.value = "Failed to load issuers";
      })
      .finally(() => {
        loading.value = false;
      });
  }, []);

  if (!IS_BROWSER) return null;

  return (
    <div class="p-6">
      <h1 class="text-2xl font-bold text-text-primary mb-1">Issuers</h1>
      <p class="text-sm text-text-muted mb-6">
        cert-manager Issuers and ClusterIssuers.
      </p>

      {loading.value && (
        <div class="flex justify-center py-12">
          <Spinner class="text-brand" />
        </div>
      )}

      {error.value && <p class="text-sm text-danger py-4">{error.value}</p>}

      {!loading.value && !error.value && issuers.value.length > 0 && (
        <div class="overflow-x-auto rounded-lg border border-border-primary">
          <table class="w-full text-sm">
            <thead>
              <tr class="border-b border-border-primary bg-surface">
                <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Name
                </th>
                <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Scope
                </th>
                <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Namespace
                </th>
                <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Type
                </th>
                <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Ready
                </th>
                <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Details
                </th>
              </tr>
            </thead>
            <tbody class="divide-y divide-border-subtle">
              {issuers.value.map((iss) => (
                <tr key={iss.uid} class="hover:bg-hover/30">
                  <td class="px-3 py-2 font-medium text-text-primary">
                    {iss.name}
                  </td>
                  <td class="px-3 py-2 text-text-secondary">{iss.scope}</td>
                  <td class="px-3 py-2 text-text-secondary">
                    {iss.namespace || "\u2014"}
                  </td>
                  <td class="px-3 py-2">
                    <IssuerTypeBadge type={iss.type} />
                  </td>
                  <td class="px-3 py-2">
                    {iss.ready
                      ? (
                        <span class="text-success text-xs font-medium">
                          Ready
                        </span>
                      )
                      : (
                        <span class="text-danger text-xs font-medium">
                          Not Ready
                        </span>
                      )}
                  </td>
                  <td class="px-3 py-2 text-text-secondary text-xs truncate max-w-[280px]">
                    {iss.acmeServer || iss.reason || "\u2014"}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {!loading.value && !error.value && issuers.value.length === 0 && (
        <div class="text-center py-12 rounded-lg border border-border-primary bg-bg-elevated">
          <p class="text-text-muted">
            No issuers found. Issuers will appear here once cert-manager is
            installed and configured.
          </p>
        </div>
      )}
    </div>
  );
}
