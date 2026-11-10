/** @jsxImportSource react */
import { useState } from "react";
import NamespaceTopology from "@/src/islands/NamespaceTopology.tsx";
import { useIsClient } from "@/src/lib/use-is-client.ts";

interface ESOChainPageProps {
  /** The page's `?namespace=` parameter, read by the server. */
  initialNamespace?: string;
}

export default function ESOChainPage({
  initialNamespace = "",
}: ESOChainPageProps) {
  const [namespace, setNamespace] = useState(initialNamespace);
  const isClient = useIsClient();

  if (!isClient) return null;

  const ns = namespace.trim();

  return (
    <div className="p-6">
      <div className="flex items-start justify-between mb-1">
        <h1 className="text-2xl font-bold text-text-primary">ESO Chain</h1>
      </div>
      <p className="text-sm text-text-muted mb-6">
        Visualize the path from a SecretStore through ExternalSecrets to the
        Pods consuming the resulting Secret.
      </p>

      <div className="space-y-4">
        <div className="flex items-center gap-3">
          <label
            className="text-sm font-medium text-text-secondary shrink-0"
            htmlFor="eso-chain-ns"
          >
            Namespace
          </label>
          <input
            id="eso-chain-ns"
            type="text"
            className="flex-1 rounded border border-border-primary px-3 py-1.5 text-sm bg-base text-text-primary"
            placeholder="e.g. payments-prod"
            value={namespace}
            aria-describedby="eso-chain-ns-hint"
            onChange={(e) => {
              setNamespace(e.currentTarget.value);
            }}
          />
        </div>
        <p id="eso-chain-ns-hint" className="sr-only">
          Namespace
        </p>

        {ns ? (
          <NamespaceTopology namespace={ns} defaultOverlay="eso-chain" />
        ) : (
          <div className="rounded-lg border border-border-primary bg-elevated p-5 text-sm text-text-muted">
            Select a namespace
          </div>
        )}
      </div>
    </div>
  );
}
