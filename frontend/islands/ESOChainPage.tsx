import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import NamespaceTopology from "@/islands/NamespaceTopology.tsx";

export default function ESOChainPage() {
  const initialNamespace = IS_BROWSER
    ? new URLSearchParams(globalThis.location.search).get("namespace") ?? ""
    : "";
  const namespace = useSignal(initialNamespace);

  if (!IS_BROWSER) return null;

  const ns = namespace.value.trim();

  return (
    <div class="p-6">
      <div class="flex items-start justify-between mb-1">
        <h1 class="text-2xl font-bold text-text-primary">ESO Chain</h1>
      </div>
      <p class="text-sm text-text-muted mb-6">
        Visualize the path from a SecretStore through ExternalSecrets to the
        Pods consuming the resulting Secret.
      </p>

      <div class="space-y-4">
        <div class="flex max-w-2xl items-center gap-3">
          <label
            class="text-sm font-medium text-text-secondary shrink-0"
            htmlFor="eso-chain-ns"
          >
            Namespace
          </label>
          <input
            id="eso-chain-ns"
            type="text"
            class="flex-1 rounded border border-border-primary px-3 py-1.5 text-sm bg-base text-text-primary"
            placeholder="e.g. payments-prod"
            value={namespace.value}
            aria-describedby="eso-chain-ns-hint"
            onInput={(e) => {
              namespace.value = (e.target as HTMLInputElement).value;
            }}
          />
        </div>
        <p id="eso-chain-ns-hint" class="text-xs text-text-muted">
          Type a namespace to render the External Secrets chain overlay.
        </p>

        {ns
          ? (
            <NamespaceTopology
              namespace={ns}
              initialOverlay="eso-chain"
              viewTopologyHref={`/observability/topology?namespace=${
                encodeURIComponent(ns)
              }&overlay=eso-chain`}
            />
          )
          : (
            <div class="flex min-h-[400px] items-center justify-center rounded-lg border border-border-primary bg-elevated p-6 text-sm text-text-muted">
              Select a namespace to view ESO chain edges.
            </div>
          )}
      </div>
    </div>
  );
}
