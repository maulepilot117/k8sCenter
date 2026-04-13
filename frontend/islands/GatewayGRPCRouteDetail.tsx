import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { useEffect } from "preact/hooks";
import { apiGet } from "@/lib/api.ts";
import { Spinner } from "@/components/ui/Spinner.tsx";
import type { GRPCRouteDetail } from "@/lib/gateway-types.ts";
import ConditionsTable from "@/components/gateway/ConditionsTable.tsx";
import ParentGatewaysTable from "@/components/gateway/ParentGatewaysTable.tsx";
import BackendRefsTable from "@/components/gateway/BackendRefsTable.tsx";

interface Props {
  namespace: string;
  name: string;
}

export default function GatewayGRPCRouteDetailIsland(
  { namespace, name }: Props,
) {
  const loading = useSignal(true);
  const error = useSignal<string | null>(null);
  const data = useSignal<GRPCRouteDetail | null>(null);

  async function fetchDetail() {
    loading.value = true;
    error.value = null;
    try {
      const res = await apiGet<GRPCRouteDetail>(
        `/v1/gateway/routes/grpcroutes/${namespace}/${name}`,
      );
      data.value = res.data ?? null;
    } catch {
      error.value = "Failed to load GRPCRoute details";
    } finally {
      loading.value = false;
    }
  }

  useEffect(() => {
    if (!IS_BROWSER) return;
    fetchDetail();
  }, [namespace, name]);

  if (!IS_BROWSER) return null;

  if (loading.value) {
    return (
      <div class="flex justify-center py-12">
        <Spinner class="text-brand" />
      </div>
    );
  }

  if (error.value) {
    return <p class="text-sm text-danger p-6">{error.value}</p>;
  }

  if (!data.value) return null;

  const detail = data.value;

  return (
    <div class="p-6 space-y-6">
      {/* Back link */}
      <a
        href="/networking/gateway-api?kind=grpcroutes"
        class="text-sm text-brand hover:underline"
      >
        &larr; Back to gRPC Routes
      </a>

      {/* Header */}
      <div>
        <h1 class="text-2xl font-bold text-text-primary">{detail.name}</h1>
        <p class="text-sm text-text-muted mt-1">
          Namespace: {detail.namespace}
        </p>
      </div>

      {/* Parent Gateways */}
      <ParentGatewaysTable parentRefs={detail.parentRefs} />

      {/* Rules */}
      {detail.rules && detail.rules.length > 0 && (
        <div class="space-y-4">
          <h2 class="text-sm font-semibold text-text-primary">
            Rules ({detail.rules.length})
          </h2>
          {detail.rules.map((rule, ri) => (
            <div
              key={ri}
              class="rounded-lg border border-border-primary bg-bg-elevated p-5 space-y-4"
            >
              <h3 class="text-sm font-medium text-text-primary">
                Rule {ri + 1}
              </h3>

              {/* Matches */}
              {rule.matches && rule.matches.length > 0 && (
                <div>
                  <h4 class="text-xs font-medium text-text-muted mb-2">
                    Matches
                  </h4>
                  <div class="rounded border border-border-primary overflow-hidden">
                    <table class="min-w-full divide-y divide-border-subtle">
                      <thead class="bg-surface">
                        <tr>
                          <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                            Service
                          </th>
                          <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                            Method
                          </th>
                          <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                            Headers
                          </th>
                        </tr>
                      </thead>
                      <tbody class="divide-y divide-border-subtle">
                        {rule.matches.map((m, mi) => (
                          <tr key={mi}>
                            <td class="px-3 py-2 text-sm text-text-primary font-mono">
                              {m.service || "\u2014"}
                            </td>
                            <td class="px-3 py-2 text-sm text-text-primary font-mono">
                              {m.method || "\u2014"}
                            </td>
                            <td class="px-3 py-2 text-sm text-text-muted">
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
