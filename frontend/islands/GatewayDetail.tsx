import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { useEffect } from "preact/hooks";
import { apiGet } from "@/lib/api.ts";
import { Spinner } from "@/components/ui/Spinner.tsx";
import { ProtocolBadge } from "@/components/ui/GatewayBadges.tsx";
import type { GatewayDetail } from "@/lib/gateway-types.ts";
import ConditionsTable from "@/components/gateway/ConditionsTable.tsx";

interface Props {
  namespace: string;
  name: string;
}

export default function GatewayDetailIsland({ namespace, name }: Props) {
  const loading = useSignal(true);
  const error = useSignal<string | null>(null);
  const data = useSignal<GatewayDetail | null>(null);

  useEffect(() => {
    if (!IS_BROWSER) return;
    async function fetch() {
      loading.value = true;
      error.value = null;
      try {
        const res = await apiGet<GatewayDetail>(
          `/v1/gateway/gateways/${namespace}/${name}`,
        );
        data.value = res.data ?? null;
      } catch {
        error.value = "Failed to load Gateway details";
      } finally {
        loading.value = false;
      }
    }
    fetch();
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

  const gw = data.value;

  return (
    <div class="p-6 space-y-6">
      <a
        href="/networking/gateway-api?kind=gateways"
        class="text-sm text-brand hover:underline"
      >
        &larr; Back to Gateways
      </a>

      <div class="flex flex-wrap items-center gap-3">
        <h2 class="text-2xl font-bold text-text-primary">{gw.name}</h2>
        <span class="rounded-full bg-surface px-2.5 py-0.5 text-xs font-medium text-text-secondary">
          {gw.namespace}
        </span>
      </div>

      {/* Details */}
      <div class="rounded-lg border border-border-primary bg-bg-elevated p-5">
        <h3 class="text-sm font-semibold text-text-primary mb-4">Details</h3>
        <dl class="grid grid-cols-2 gap-4 text-sm">
          <div>
            <dt class="text-text-muted">Gateway Class</dt>
            <dd>
              <a
                href={`/networking/gateway-api/gatewayclasses/${gw.gatewayClassName}`}
                class="text-brand hover:underline"
              >
                {gw.gatewayClassName}
              </a>
            </dd>
          </div>
          <div>
            <dt class="text-text-muted">Addresses</dt>
            <dd class="text-text-primary">
              {(gw.addresses ?? []).join(", ") || "-"}
            </dd>
          </div>
          <div>
            <dt class="text-text-muted">Age</dt>
            <dd class="text-text-primary">{gw.age}</dd>
          </div>
        </dl>
      </div>

      {/* Listeners */}
      {gw.listeners && gw.listeners.length > 0 && (
        <div class="rounded-lg border border-border-primary">
          <h3 class="text-sm font-semibold text-text-primary px-4 py-3 border-b border-border-primary">
            Listeners ({gw.listeners.length})
          </h3>
          <div class="overflow-x-auto">
            <table class="w-full text-sm">
              <thead>
                <tr class="border-b border-border-primary bg-surface">
                  <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                    Name
                  </th>
                  <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                    Port
                  </th>
                  <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                    Protocol
                  </th>
                  <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                    Hostname
                  </th>
                  <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                    TLS Mode
                  </th>
                  <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                    Cert Ref
                  </th>
                  <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                    Attached Routes
                  </th>
                  <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                    Status
                  </th>
                </tr>
              </thead>
              <tbody class="divide-y divide-border-subtle">
                {gw.listeners.map((l) => {
                  const accepted = l.conditions?.find(
                    (c) => c.type === "Accepted",
                  );
                  const isHealthy = accepted?.status === "True";
                  return (
                    <tr key={l.name} class="hover:bg-hover/30">
                      <td class="px-3 py-2 font-medium text-text-primary">
                        {l.name}
                      </td>
                      <td class="px-3 py-2 text-text-secondary">{l.port}</td>
                      <td class="px-3 py-2">
                        <ProtocolBadge protocol={l.protocol} />
                      </td>
                      <td class="px-3 py-2 text-text-secondary">
                        {l.hostname || "*"}
                      </td>
                      <td class="px-3 py-2 text-text-secondary">
                        {l.tlsMode || "-"}
                      </td>
                      <td class="px-3 py-2 text-text-secondary">
                        {l.certificateRef || "-"}
                      </td>
                      <td class="px-3 py-2 text-text-secondary">
                        {l.attachedRouteCount}
                      </td>
                      <td class="px-3 py-2">
                        {accepted
                          ? (
                            <span
                              class={isHealthy ? "text-success" : "text-danger"}
                            >
                              {isHealthy
                                ? "Accepted"
                                : accepted.reason || "Not Accepted"}
                            </span>
                          )
                          : <span class="text-text-muted">-</span>}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {/* Attached Routes */}
      {gw.attachedRoutes && gw.attachedRoutes.length > 0 && (
        <div class="rounded-lg border border-border-primary">
          <h3 class="text-sm font-semibold text-text-primary px-4 py-3 border-b border-border-primary">
            Attached Routes ({gw.attachedRoutes.length})
          </h3>
          <div class="overflow-x-auto">
            <table class="w-full text-sm">
              <thead>
                <tr class="border-b border-border-primary bg-surface">
                  <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                    Kind
                  </th>
                  <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                    Name
                  </th>
                  <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                    Namespace
                  </th>
                </tr>
              </thead>
              <tbody class="divide-y divide-border-subtle">
                {gw.attachedRoutes.map((r) => (
                  <tr
                    key={`${r.kind}-${r.namespace}-${r.name}`}
                    class="hover:bg-hover/30"
                  >
                    <td class="px-3 py-2 text-text-secondary">{r.kind}</td>
                    <td class="px-3 py-2">
                      <a
                        href={`/networking/gateway-api/${r.kind.toLowerCase()}s/${r.namespace}/${r.name}`}
                        class="text-brand hover:underline font-medium"
                      >
                        {r.name}
                      </a>
                    </td>
                    <td class="px-3 py-2 text-text-secondary">
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
