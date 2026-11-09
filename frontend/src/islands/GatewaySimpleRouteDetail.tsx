/** @jsxImportSource react */
import { useEffect, useState } from "react";
import BackendRefsTable from "@/components/gateway/BackendRefsTable.tsx";
import ConditionsTable from "@/components/gateway/ConditionsTable.tsx";
import ParentGatewaysTable from "@/components/gateway/ParentGatewaysTable.tsx";
import { Spinner } from "@/components/ui/Spinner.tsx";
import { apiGet } from "@/lib/api.ts";
import type { SimpleRouteDetail } from "@/lib/gateway-types.ts";
import { IS_BROWSER } from "@/src/lib/is-browser.ts";
import { useIsClient } from "@/src/lib/use-is-client.ts";

interface Props {
  kind: string;
  namespace: string;
  name: string;
}

const KIND_LABELS: Record<string, string> = {
  tcproutes: "TCP Route",
  tlsroutes: "TLS Route",
  udproutes: "UDP Route",
};

export default function GatewaySimpleRouteDetailIsland({
  kind,
  namespace,
  name,
}: Props) {
  const isClient = useIsClient();
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [data, setData] = useState<SimpleRouteDetail | null>(null);

  const kindLabel = KIND_LABELS[kind] ?? kind;

  useEffect(() => {
    if (!IS_BROWSER) return;
    async function fetchDetail() {
      setLoading(true);
      setError(null);
      try {
        const res = await apiGet<SimpleRouteDetail>(
          `/v1/gateway/routes/${kind}/${namespace}/${name}`,
        );
        setData(res.data ?? null);
      } catch {
        setError(`Failed to load ${kindLabel} details`);
      } finally {
        setLoading(false);
      }
    }
    fetchDetail();
  }, [kind, kindLabel, namespace, name]);

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
        href={`/networking/gateway-api?kind=${kind}`}
        className="text-sm text-brand hover:underline"
      >
        &larr; Back to {kindLabel}s
      </a>

      {/* Header */}
      <div className="flex flex-wrap items-center gap-3">
        <h1 className="text-2xl font-bold text-text-primary">{detail.name}</h1>
        <span className="inline-block rounded-full px-3 py-1 text-xs font-medium bg-elevated border border-border-primary text-text-secondary">
          {kindLabel}
        </span>
      </div>
      <p className="text-sm text-text-muted">Namespace: {detail.namespace}</p>

      {/* Hostnames (TLS only) */}
      {detail.hostnames && detail.hostnames.length > 0 && (
        <div>
          <h2 className="text-sm font-semibold text-text-primary mb-2">
            Hostnames
          </h2>
          <div className="flex flex-wrap gap-2">
            {detail.hostnames.map((h) => (
              <span
                key={h}
                className="inline-block rounded-full px-3 py-1 text-xs font-medium bg-elevated border border-border-primary text-text-secondary"
              >
                {h}
              </span>
            ))}
          </div>
        </div>
      )}

      {/* Parent Gateways */}
      <ParentGatewaysTable parentRefs={detail.parentRefs} />

      {/* Backend References */}
      <BackendRefsTable backendRefs={detail.backendRefs} />

      {/* Conditions */}
      <ConditionsTable conditions={detail.conditions} />
    </div>
  );
}
