/** @jsxImportSource react */
import { useEffect, useState } from "react";
import BackendRefsTable from "@/components/gateway/BackendRefsTable.tsx";
import ConditionsTable from "@/components/gateway/ConditionsTable.tsx";
import ParentGatewaysTable from "@/components/gateway/ParentGatewaysTable.tsx";
import { Spinner } from "@/components/ui/Spinner.tsx";
import { apiGet } from "@/lib/api.ts";
import type { GRPCRouteDetail } from "@/lib/gateway-types.ts";
import { IS_BROWSER } from "@/src/lib/is-browser.ts";
import { useIsClient } from "@/src/lib/use-is-client.ts";

interface Props {
  namespace: string;
  name: string;
}

export default function GatewayGRPCRouteDetailIsland({
  namespace,
  name,
}: Props) {
  const isClient = useIsClient();
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [data, setData] = useState<GRPCRouteDetail | null>(null);

  useEffect(() => {
    if (!IS_BROWSER) return;
    async function fetchDetail() {
      setLoading(true);
      setError(null);
      try {
        const res = await apiGet<GRPCRouteDetail>(
          `/v1/gateway/routes/grpcroutes/${namespace}/${name}`,
        );
        setData(res.data ?? null);
      } catch {
        setError("Failed to load GRPCRoute details");
      } finally {
        setLoading(false);
      }
    }
    fetchDetail();
  }, [namespace, name]);

  if (!isClient) return null;

  if (loading) {
    return (
      <div className="flex justify-center py-12">
        <Spinner className="text-brand" />
      </div>
    );
  }

  if (error) {
    return <p className="text-sm text-danger p-6">{error}</p>;
  }

  if (!data) return null;

  const detail = data;

  return (
    <div className="p-6 space-y-6">
      {/* Back link */}
      <a
        href="/networking/gateway-api?kind=grpcroutes"
        className="text-sm text-brand hover:underline"
      >
        &larr; Back to gRPC Routes
      </a>

      {/* Header */}
      <div>
        <h1 className="text-2xl font-bold text-text-primary">{detail.name}</h1>
        <p className="text-sm text-text-muted mt-1">
          Namespace: {detail.namespace}
        </p>
      </div>

      {/* Parent Gateways */}
      <ParentGatewaysTable parentRefs={detail.parentRefs} />

      {/* Rules */}
      {detail.rules && detail.rules.length > 0 && (
        <div className="space-y-4">
          <h2 className="text-sm font-semibold text-text-primary">
            Rules ({detail.rules.length})
          </h2>
          {detail.rules.map((rule, ri) => (
            <div
              // biome-ignore lint/suspicious/noArrayIndexKey: rules are positional entries with no id, and the detail is replaced whole on each fetch
              key={ri}
              className="rounded-lg border border-border-primary bg-elevated p-5 space-y-4"
            >
              <h3 className="text-sm font-medium text-text-primary">
                Rule {ri + 1}
              </h3>

              {/* Matches */}
              {rule.matches && rule.matches.length > 0 && (
                <div>
                  <h4 className="text-xs font-medium text-text-muted mb-2">
                    Matches
                  </h4>
                  <div className="rounded border border-border-primary overflow-hidden">
                    <table className="min-w-full divide-y divide-border-subtle">
                      <thead className="bg-surface">
                        <tr>
                          <th className="px-3 py-2 text-left text-xs font-medium text-text-muted">
                            Service
                          </th>
                          <th className="px-3 py-2 text-left text-xs font-medium text-text-muted">
                            Method
                          </th>
                          <th className="px-3 py-2 text-left text-xs font-medium text-text-muted">
                            Headers
                          </th>
                        </tr>
                      </thead>
                      <tbody className="divide-y divide-border-subtle">
                        {rule.matches.map((m, mi) => (
                          // biome-ignore lint/suspicious/noArrayIndexKey: matches are positional entries with no id, and the detail is replaced whole on each fetch
                          <tr key={mi}>
                            <td className="px-3 py-2 text-sm text-text-primary font-mono">
                              {m.service || "\u2014"}
                            </td>
                            <td className="px-3 py-2 text-sm text-text-primary font-mono">
                              {m.method || "\u2014"}
                            </td>
                            <td className="px-3 py-2 text-sm text-text-muted">
                              {m.headers && m.headers.length > 0
                                ? m.headers.join(", ")
                                : "\u2014"}
                            </td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                </div>
              )}

              {/* Backends */}
              <BackendRefsTable backendRefs={rule.backendRefs} />
            </div>
          ))}
        </div>
      )}

      {/* Conditions */}
      <ConditionsTable conditions={detail.conditions} />
    </div>
  );
}
