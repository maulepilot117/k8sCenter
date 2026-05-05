import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import NamespaceTopology from "@/islands/NamespaceTopology.tsx";
import { selectedNamespace } from "@/lib/namespace.ts";

interface Props {
  namespace?: string;
  focusedNodeId: string;
}

export default function ESOChainPanel({ namespace, focusedNodeId }: Props) {
  const nsInput = useSignal(
    namespace ??
      (selectedNamespace.value !== "all" ? selectedNamespace.value : ""),
  );

  if (!IS_BROWSER) return null;

  const ns = (namespace ?? nsInput.value).trim();
  const topologyHref = ns
    ? `/observability/topology?namespace=${
      encodeURIComponent(ns)
    }&overlay=eso-chain&focus=${encodeURIComponent(focusedNodeId)}`
    : "/external-secrets/chain";

  return (
    <div role="tabpanel" class="space-y-3">
      <div class="flex flex-wrap items-center gap-3">
        {!namespace && (
          <input
            type="text"
            class="w-64 max-w-full rounded border border-border-primary bg-base px-3 py-1.5 text-sm text-text-primary"
            placeholder="Namespace"
            value={nsInput.value}
            onInput={(e) => {
              nsInput.value = (e.currentTarget as HTMLInputElement).value;
            }}
          />
        )}
        <a
          href={topologyHref}
          class="ml-auto rounded border border-border-primary px-3 py-1.5 text-sm text-text-primary hover:bg-base"
        >
          View in Topology
        </a>
      </div>

      {ns
        ? (
          <NamespaceTopology
            namespace={ns}
            defaultOverlay="eso-chain"
            focusedNodeId={focusedNodeId}
            embedded
            minHeight={400}
          />
        )
        : (
          <div class="rounded-lg border border-border-primary bg-elevated p-5 text-sm text-text-muted">
            Select a namespace to inspect this ClusterSecretStore chain.
          </div>
        )}
    </div>
  );
}
