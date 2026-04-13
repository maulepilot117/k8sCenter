import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { useEffect } from "preact/hooks";
import { apiGet } from "@/lib/api.ts";
import { Spinner } from "@/components/ui/Spinner.tsx";
import type { SimpleRouteDetail } from "@/lib/gateway-types.ts";
import ConditionsTable from "@/components/gateway/ConditionsTable.tsx";
import ParentGatewaysTable from "@/components/gateway/ParentGatewaysTable.tsx";
import BackendRefsTable from "@/components/gateway/BackendRefsTable.tsx";

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

export default function GatewaySimpleRouteDetailIsland(
  { kind, namespace, name }: Props,
) {
  const loading = useSignal(true);
  const error = useSignal<string | null>(null);
  const data = useSignal<SimpleRouteDetail | null>(null);

  const kindLabel = KIND_LABELS[kind] ?? kind;

  async function fetchDetail() {
    loading.value = true;
    error.value = null;
    try {
      const res = await apiGet<SimpleRouteDetail>(
        `/v1/gateway/routes/${kind}/${namespace}/${name}`,
      );
      data.value = res.data ?? null;
    } catch {
      error.value = `Failed to load ${kindLabel} details`;
    } finally {
      loading.value = false;
    }
  }

  useEffect(() => {
    if (!IS_BROWSER) return;
    fetchDetail();
  }, [kind, namespace, name]);

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
        href={`/networking/gateway-api?kind=${kind}`}
        class="text-sm text-brand hover:underline"
      >
        &larr; Back to {kindLabel}s
      </a>

      {/* Header */}
      <div class="flex flex-wrap items-center gap-3">
        <h1 class="text-2xl font-bold text-text-primary">{detail.name}</h1>
        <span class="inline-block rounded-full px-3 py-1 text-xs font-medium bg-bg-elevated border border-border-primary text-text-secondary">
          {kindLabel}
        </span>
      </div>
      <p class="text-sm text-text-muted">
        Namespace: {detail.namespace}
      </p>

      {/* Hostnames (TLS only) */}
      {detail.hostnames && detail.hostnames.length > 0 && (
        <div>
          <h2 class="text-sm font-semibold text-text-primary mb-2">
            Hostnames
          </h2>
          <div class="flex flex-wrap gap-2">
            {detail.hostnames.map((h) => (
              <span
                key={h}
                class="inline-block rounded-full px-3 py-1 text-xs font-medium bg-bg-elevated border border-border-primary text-text-secondary"
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
