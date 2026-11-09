/** @jsxImportSource react */
import { Card } from "@/components/ui/Card.tsx";
import StatusBadge from "@/components/ui/glass/StatusBadge.tsx";
import { PanelStateNotice } from "@/components/ui/PanelStateNotice.tsx";
import type { CiliumIPAMResponse } from "@/lib/cilium-types.ts";
import { usePoll } from "@/lib/hooks/use-poll.ts";
import { panelState } from "@/lib/networking-state.ts";
import { useIsClient } from "@/src/lib/use-is-client.ts";

export default function IpamStatus() {
  const { data, loading, error, failure, refetch, lastFetchedAt } =
    usePoll<CiliumIPAMResponse>("/v1/networking/cilium/ipam", {
      interval: 60_000,
      shouldContinuePolling: (d) => d.configured,
    });
  const isClient = useIsClient();

  if (!isClient || loading) {
    return <div className="animate-pulse h-40 bg-elevated rounded-lg" />;
  }

  if (error) {
    return (
      <Card title="IP Address Management">
        <PanelStateNotice
          state={panelState(failure, "IPAM data")}
          onRetry={refetch}
        />
      </Card>
    );
  }

  const resp = data;
  if (!resp?.configured) {
    return (
      <Card title="IP Address Management" className="opacity-50">
        <p className="text-sm text-text-muted">IPAM data not available.</p>
      </Card>
    );
  }

  const allocated = resp.allocated ?? 0;
  const total = resp.total ?? 0;

  const pct = total > 0 ? Math.round((allocated / total) * 100) : 0;

  return (
    <Card title="IP Address Management">
      <div className="space-y-3">
        <div className="flex justify-between">
          <span className="text-text-muted">Mode</span>
          <span className="text-text-primary font-medium capitalize">
            {resp.mode.replace(/-/g, " ")}
          </span>
        </div>
        {resp.podCIDRs.length > 0 && (
          <div className="flex justify-between">
            <span className="text-text-muted">Pod CIDR</span>
            <span className="font-mono text-sm text-text-primary">
              {resp.podCIDRs[0]}
              {resp.podCIDRs.length > 1 && (
                <span className="text-text-muted">
                  &nbsp;+{resp.podCIDRs.length - 1}
                </span>
              )}
            </span>
          </div>
        )}
        <div className="flex justify-between">
          <span className="text-text-muted">Allocated</span>
          <span className="font-mono text-sm text-text-primary">
            {allocated.toLocaleString()} / {total.toLocaleString()}
          </span>
        </div>
        {/* Progress bar */}
        <div className="w-full bg-elevated rounded-full h-2">
          <div
            className="h-2 rounded-full"
            style={{
              width: `${Math.max(pct, 1)}%`,
              background:
                resp.exhaustionRisk === "high"
                  ? "var(--error)"
                  : resp.exhaustionRisk === "medium"
                    ? "var(--warning)"
                    : "var(--accent)",
              minWidth: allocated > 0 ? "4px" : "0",
            }}
          />
        </div>
        <div className="flex justify-between">
          <span className="text-text-muted">Exhaustion Risk</span>
          <StatusBadge
            label={
              resp.exhaustionRisk === "none"
                ? "None"
                : resp.exhaustionRisk.charAt(0).toUpperCase() +
                  resp.exhaustionRisk.slice(1)
            }
            tone={
              resp.exhaustionRisk === "high"
                ? "crit"
                : resp.exhaustionRisk === "medium"
                  ? "warn"
                  : "ok"
            }
          />
        </div>

        {/* Per-node breakdown */}
        {resp.perNode.length > 0 && (
          <div className="mt-4 pt-3 border-t border-border-subtle">
            <p className="text-xs font-medium text-text-muted uppercase tracking-wider mb-2">
              Per Node
            </p>
            <div className="space-y-1.5">
              {resp.perNode.map((node) => (
                <div key={node.node} className="flex justify-between text-xs">
                  <span className="font-mono text-text-secondary">
                    {node.node}
                  </span>
                  <span className="text-text-muted">
                    {node.allocated} used / {node.allocated + node.available}{" "}
                    total
                  </span>
                </div>
              ))}
            </div>
          </div>
        )}
      </div>
      {lastFetchedAt && (
        <div className="mt-3 pt-2 border-t border-border-subtle text-right">
          <span className="text-xs text-text-muted">
            Updated {lastFetchedAt.toLocaleTimeString()}
          </span>
        </div>
      )}
    </Card>
  );
}
