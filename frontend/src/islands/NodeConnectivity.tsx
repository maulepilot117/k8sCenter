import { Card } from "@/components/ui/Card.tsx";
import { PanelStateNotice } from "@/components/ui/PanelStateNotice.tsx";
import type {
  CiliumConnectivityResponse,
  NodeConnectivity as NodeHealth,
} from "@/lib/cilium-types.ts";
import { usePoll } from "@/lib/hooks/use-poll.ts";
import { panelState } from "@/lib/networking-state.ts";
import { IS_BROWSER } from "@/src/lib/is-browser.ts";

/**
 * What a remote exec failure means for a node's row. The agent could not be
 * asked, so the node's health is unknown, not failed (#608 R8).
 */
function execProblem(execError: string): string {
  switch (execError) {
    case "forbidden":
      return "exec into the agent is not permitted";
    case "timeout":
      return "the agent timed out";
    case "pod_gone":
      return "the agent pod is gone";
    default:
      return "exec into the agent failed";
  }
}

export default function NodeConnectivity() {
  const { data, loading, error, failure, refetch, lastFetchedAt } =
    usePoll<CiliumConnectivityResponse>("/v1/networking/cilium/connectivity", {
      interval: 60_000,
      shouldContinuePolling: (d) => d.configured,
    });

  if (!IS_BROWSER || loading.value) {
    return <div class="animate-pulse h-24 bg-elevated rounded-lg" />;
  }

  if (error.value) {
    return (
      <Card title="Node Connectivity">
        <PanelStateNotice
          state={panelState(failure.value, "node connectivity")}
          onRetry={refetch}
        />
      </Card>
    );
  }

  const resp = data.value;
  if (!resp?.configured) {
    return (
      <Card title="Node Connectivity" class="opacity-50">
        <p class="text-sm text-text-muted">Connectivity data not available.</p>
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
            data-node={node.nodeName}
            class="flex items-center justify-between text-sm"
          >
            <div class="flex items-center gap-2">
              <span
                class="w-2 h-2 rounded-full inline-block"
                style={{ background: healthColor(node.healthState) }}
              />
              <span class="font-mono text-text-primary">{node.nodeName}</span>
            </div>
            {node.execError ? (
              <span class="text-xs text-warning">
                Exec problem: {execProblem(node.execError)}
              </span>
            ) : (
              <div class="flex items-center gap-2">
                <span class="text-xs text-text-muted">{node.healthState}</span>
                {node.message && (
                  <span class="text-xs text-text-muted">— {node.message}</span>
                )}
              </div>
            )}
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
