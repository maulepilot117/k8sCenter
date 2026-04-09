import { IS_BROWSER } from "fresh/runtime";
import { usePoll } from "@/lib/hooks/use-poll.ts";
import type {
  CiliumConnectivityResponse,
  NodeConnectivity as NodeHealth,
} from "@/lib/cilium-types.ts";
import { Card } from "@/components/ui/Card.tsx";
import { ErrorBanner } from "@/components/ui/ErrorBanner.tsx";

export default function NodeConnectivity() {
  const { data, loading, error, lastFetchedAt } = usePoll<
    CiliumConnectivityResponse
  >(
    "/v1/networking/cilium/connectivity",
    {
      interval: 60_000,
      shouldContinuePolling: (d) => d.configured,
    },
  );

  if (!IS_BROWSER || loading.value) {
    return <div class="animate-pulse h-24 bg-elevated rounded-lg" />;
  }

  if (error.value) {
    return (
      <Card title="Node Connectivity">
        <ErrorBanner message={error.value} />
      </Card>
    );
  }

  const resp = data.value;
  if (!resp || !resp.configured) {
    return (
      <Card title="Node Connectivity" class="opacity-50">
        <p class="text-sm text-text-muted">
          Connectivity data not available.
        </p>
      </Card>
    );
  }

  // Exec disabled — show placeholder
  if (resp.nodes.length === 0) {
    return (
      <Card title="Node Connectivity">
        <p class="text-sm text-text-muted">
          Enable{" "}
          <code class="text-xs bg-elevated px-1 py-0.5 rounded">
            ciliumAgent.execEnabled
          </code>{" "}
          in Helm values for node connectivity data.
        </p>
      </Card>
    );
  }

  const healthColor = (state: string) => {
    switch (state) {
      case "Ok":
        return "var(--success)";
      case "Warning":
        return "var(--warning)";
      case "Failure":
        return "var(--error)";
      default:
        return "var(--text-muted)";
    }
  };

  return (
    <Card title="Node Connectivity">
      <div class="space-y-1.5">
        {resp.nodes.map((node: NodeHealth) => (
          <div
            key={node.nodeName}
            class="flex items-center justify-between text-sm"
          >
            <div class="flex items-center gap-2">
              <span
                class="w-2 h-2 rounded-full inline-block"
                style={{ background: healthColor(node.healthState) }}
              />
              <span class="font-mono text-text-primary">
                {node.nodeName}
              </span>
            </div>
            <div class="flex items-center gap-2">
              <span class="text-xs text-text-muted">
                {node.healthState}
              </span>
              {node.message && (
                <span class="text-xs text-text-muted">
                  — {node.message}
                </span>
              )}
            </div>
          </div>
        ))}
      </div>
      {resp.partial && (
        <p class="text-xs text-text-muted mt-2">
          Partial data — some agent pods failed to respond.
        </p>
      )}
      {lastFetchedAt.value && (
        <div class="mt-3 pt-2 border-t border-border-subtle text-right">
          <span class="text-xs text-text-muted">
            Updated {lastFetchedAt.value.toLocaleTimeString()}
          </span>
        </div>
      )}
    </Card>
  );
}
