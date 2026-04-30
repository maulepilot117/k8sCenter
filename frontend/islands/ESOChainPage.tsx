import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";

/** Chain page (Phase B placeholder).
 *
 * The cross-resource overlay (ExternalSecret → SecretStore → target Secret →
 * consuming Pods) ships in Phase I as a `?overlay=eso-chain` query on the
 * existing `/observability/topology` view. Until then this page exists so the
 * "Chain" tab in the External Secrets nav has a real landing surface and a
 * one-click jump into the namespaced topology view. */
export default function ESOChainPage() {
  const namespace = useSignal("");

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

      <div class="rounded-lg border border-border-primary bg-elevated p-6 max-w-2xl">
        <h2 class="text-base font-semibold text-text-primary mb-2">
          Chain overlay coming in Phase I
        </h2>
        <p class="text-sm text-text-muted mb-4">
          The dedicated overlay is part of a later phase. In the meantime, the
          existing namespace topology view shows the same resources — pick a
          namespace below to jump into it.
        </p>

        <div class="flex items-center gap-3 mb-4">
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
        <p id="eso-chain-ns-hint" class="text-xs text-text-muted mb-4">
          Type a namespace, then click "Open in Topology" to view its
          ExternalSecrets in context.
        </p>

        {ns
          ? (
            <a
              href={`/observability/topology?namespace=${
                encodeURIComponent(ns)
              }`}
              class="inline-flex items-center gap-2 rounded-md bg-brand px-3 py-1.5 text-sm font-medium text-white hover:bg-brand/90"
            >
              Open in Topology →
            </a>
          )
          : (
            <button
              type="button"
              class="inline-flex items-center gap-2 rounded-md border border-border-primary px-3 py-1.5 text-sm font-medium text-text-muted cursor-not-allowed opacity-60"
              disabled
              aria-disabled="true"
            >
              Select a namespace
            </button>
          )}
      </div>
    </div>
  );
}
