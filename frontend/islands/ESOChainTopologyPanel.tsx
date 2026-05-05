import { selectedNamespace } from "@/lib/namespace.ts";
import NamespaceTopology from "@/islands/NamespaceTopology.tsx";

interface Props {
  kind: "SecretStore" | "ClusterSecretStore" | "ExternalSecret";
  namespace?: string;
  name: string;
}

export default function ESOChainTopologyPanel(
  { kind, namespace, name }: Props,
) {
  const topologyNamespace = namespace ?? selectedNamespace.value;

  if (topologyNamespace === "all") {
    return (
      <div class="flex min-h-[400px] items-center justify-center rounded-lg border border-border-primary bg-elevated p-6 text-sm text-text-muted">
        Select a namespace to view this cluster-scoped chain.
      </div>
    );
  }

  const params = new URLSearchParams({
    namespace: topologyNamespace,
    overlay: "eso-chain",
    focusKind: kind,
    focusName: name,
  });
  if (namespace) params.set("focusNamespace", namespace);

  return (
    <NamespaceTopology
      namespace={topologyNamespace}
      initialOverlay="eso-chain"
      focusedNode={{ kind, namespace, name }}
      embedded
      minHeight={400}
      viewTopologyHref={`/observability/topology?${params.toString()}`}
    />
  );
}
