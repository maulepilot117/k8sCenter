import { useEffect, useState } from "react";
import ConditionsTable from "@/components/gateway/ConditionsTable.tsx";
import { ProtocolBadge } from "@/components/ui/GatewayBadges.tsx";
import { SourceCoverageNotice } from "@/components/ui/SourceCoverageNotice.tsx";
import { Spinner } from "@/components/ui/Spinner.tsx";
import { apiGet } from "@/lib/api.ts";
import { age } from "@/lib/format.ts";
import {
  GATEWAY_KIND_LABELS,
  type GatewayDetail,
} from "@/lib/gateway-types.ts";
import { IS_BROWSER } from "@/src/lib/is-browser.ts";
import { useIsClient } from "@/src/lib/use-is-client.ts";

interface Props {
  namespace: string;
  name: string;
}

export default function GatewayDetailIsland({ namespace, name }: Props) {
  const isClient = useIsClient();
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [data, setData] = useState<GatewayDetail | null>(null);

  useEffect(() => {
    if (!IS_BROWSER) return;
    async function fetch() {
      setLoading(true);
      setError(null);
      try {
        const res = await apiGet<GatewayDetail>(
          `/v1/gateway/gateways/${namespace}/${name}`,
        );
        setData(res.data ?? null);
      } catch {
        setError("Failed to load Gateway details");
      } finally {
        setLoading(false);
      }
    }
    fetch();
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

  const gw = data;

  return (
    <div className="p-6 space-y-6">
      <a
        href="/networking/gateway-api?kind=gateways"
        className="text-sm text-brand hover:underline"
      >
        &larr; Back to Gateways
      </a>

      <div className="flex flex-wrap items-center gap-3">
        <h2 className="text-2xl font-bold text-text-primary">{gw.name}</h2>
        <span className="rounded-full bg-surface px-2.5 py-0.5 text-xs font-medium text-text-secondary">
          {gw.namespace}
        </span>
      </div>

      {/* Details */}
      <div className="rounded-lg border border-border-primary bg-elevated p-5">
        <h3 className="text-sm font-semibold text-text-primary mb-4">
          Details
        </h3>
        <dl className="grid grid-cols-2 gap-4 text-sm">
          <div>
            <dt className="text-text-muted">Gateway Class</dt>
            <dd>
              <a
                href={`/networking/gateway-api/gatewayclasses/${gw.gatewayClassName}`}
                className="text-brand hover:underline"
              >
                {gw.gatewayClassName}
              </a>
            </dd>
          </div>
          <div>
            <dt className="text-text-muted">Addresses</dt>
            <dd className="text-text-primary">
              {(gw.addresses ?? []).join(", ") || "-"}
            </dd>
          </div>
          <div>
            <dt className="text-text-muted">Age</dt>
            <dd className="text-text-primary" title={gw.age}>
              {gw.age ? age(gw.age) : "-"}
            </dd>
          </div>
        </dl>
      </div>

      {/* Listeners */}
      {gw.listeners && gw.listeners.length > 0 && (
        <div className="rounded-lg border border-border-primary">
          <h3 className="text-sm font-semibold text-text-primary px-4 py-3 border-b border-border-primary">
            Listeners ({gw.listeners.length})
          </h3>
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-border-primary bg-surface">
                  <th className="px-3 py-2 text-left text-xs font-medium text-text-muted">
                    Name
                  </th>
                  <th className="px-3 py-2 text-left text-xs font-medium text-text-muted">
                    Port
                  </th>
                  <th className="px-3 py-2 text-left text-xs font-medium text-text-muted">
                    Protocol
                  </th>
                  <th className="px-3 py-2 text-left text-xs font-medium text-text-muted">
                    Hostname
                  </th>
                  <th className="px-3 py-2 text-left text-xs font-medium text-text-muted">
                    TLS Mode
                  </th>
                  <th className="px-3 py-2 text-left text-xs font-medium text-text-muted">
                    Cert Ref
                  </th>
                  <th className="px-3 py-2 text-left text-xs font-medium text-text-muted">
                    Attached Routes
                  </th>
                  <th className="px-3 py-2 text-left text-xs font-medium text-text-muted">
                    Status
                  </th>
                </tr>
              </thead>
              <tbody className="divide-y divide-border-subtle">
                {gw.listeners.map((l) => {
                  const accepted = l.conditions?.find(
                    (c) => c.type === "Accepted",
                  );
                  const isHealthy = accepted?.status === "True";
                  return (
                    <tr key={l.name} className="hover:bg-hover/30">
                      <td className="px-3 py-2 font-medium text-text-primary">
                        {l.name}
                      </td>
                      <td className="px-3 py-2 text-text-secondary">
                        {l.port}
                      </td>
                      <td className="px-3 py-2">
                        <ProtocolBadge protocol={l.protocol} />
                      </td>
                      <td className="px-3 py-2 text-text-secondary">
                        {l.hostname || "*"}
                      </td>
                      <td className="px-3 py-2 text-text-secondary">
                        {l.tlsMode || "-"}
                      </td>
                      <td className="px-3 py-2 text-text-secondary">
                        {l.certificateRef || "-"}
                      </td>
                      <td className="px-3 py-2 text-text-secondary">
                        {l.attachedRouteCount}
                      </td>
                      <td className="px-3 py-2">
                        {accepted ? (
                          <span
                            className={
                              isHealthy ? "text-success" : "text-danger"
                            }
                          >
                            {isHealthy
                              ? "Accepted"
                              : accepted.reason || "Not Accepted"}
                          </span>
                        ) : (
                          <span className="text-text-muted">-</span>
                        )}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {/* A remote route kind that could not be listed is missing from the
          attached routes below; name it, even when none are attached. */}
      <SourceCoverageNotice
        coverage={gw.coverage}
        labels={GATEWAY_KIND_LABELS}
      />

      {/* Attached Routes */}
      {gw.attachedRoutes && gw.attachedRoutes.length > 0 && (
        <div className="rounded-lg border border-border-primary">
          <h3 className="text-sm font-semibold text-text-primary px-4 py-3 border-b border-border-primary">
            Attached Routes ({gw.attachedRoutes.length})
          </h3>
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-border-primary bg-surface">
                  <th className="px-3 py-2 text-left text-xs font-medium text-text-muted">
                    Kind
                  </th>
                  <th className="px-3 py-2 text-left text-xs font-medium text-text-muted">
                    Name
                  </th>
                  <th className="px-3 py-2 text-left text-xs font-medium text-text-muted">
                    Namespace
                  </th>
                </tr>
              </thead>
              <tbody className="divide-y divide-border-subtle">
                {gw.attachedRoutes.map((r) => (
                  <tr
                    key={`${r.kind}-${r.namespace}-${r.name}`}
                    className="hover:bg-hover/30"
                  >
                    <td className="px-3 py-2 text-text-secondary">{r.kind}</td>
                    <td className="px-3 py-2">
                      <a
                        href={`/networking/gateway-api/${r.kind.toLowerCase()}s/${r.namespace}/${r.name}`}
                        className="text-brand hover:underline font-medium"
                      >
                        {r.name}
                      </a>
                    </td>
                    <td className="px-3 py-2 text-text-secondary">
                      {r.namespace}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}

      <ConditionsTable conditions={gw.conditions} />
    </div>
  );
}
