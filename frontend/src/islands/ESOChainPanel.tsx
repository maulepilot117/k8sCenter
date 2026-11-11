import { useState } from "react";
import NamespaceTopology from "@/src/islands/NamespaceTopology.tsx";
import { selectedNamespace } from "@/src/lib/namespace.ts";
import { useIsClient } from "@/src/lib/use-is-client.ts";

interface Props {
  namespace?: string;
  focusedNodeId: string;
}

export default function ESOChainPanel({ namespace, focusedNodeId }: Props) {
  const [nsInput, setNsInput] = useState(
    () =>
      namespace ??
      (selectedNamespace.value !== "all" ? selectedNamespace.value : ""),
  );
  const isClient = useIsClient();

  if (!isClient) return null;

  const ns = (namespace ?? nsInput).trim();
  const topologyHref = ns
    ? `/observability/topology?namespace=${encodeURIComponent(
        ns,
      )}&overlay=eso-chain&focus=${encodeURIComponent(focusedNodeId)}`
    : "/external-secrets/chain";

  return (
    <div role="tabpanel" className="space-y-3">
      <div className="flex flex-wrap items-center gap-3">
        {!namespace && (
          <input
            type="text"
            className="w-64 max-w-full rounded border border-border-primary bg-base px-3 py-1.5 text-sm text-text-primary"
            placeholder="Namespace"
            value={nsInput}
            onChange={(e) => {
              setNsInput(e.currentTarget.value);
            }}
          />
        )}
        <a
          href={topologyHref}
          className="ml-auto rounded border border-border-primary px-3 py-1.5 text-sm text-text-primary hover:bg-base"
        >
          View in Topology
        </a>
      </div>

      {ns ? (
        <NamespaceTopology
          namespace={ns}
          defaultOverlay="eso-chain"
          focusedNodeId={focusedNodeId}
          embedded
          minHeight={400}
        />
      ) : (
        <div className="rounded-lg border border-border-primary bg-elevated p-5 text-sm text-text-muted">
          Select a namespace to inspect this ClusterSecretStore chain.
        </div>
      )}
    </div>
  );
}
