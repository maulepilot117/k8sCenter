import { IS_BROWSER } from "fresh/runtime";
import { usePoll } from "@/lib/hooks/use-poll.ts";
import type { CiliumIPAMResponse } from "@/lib/cilium-types.ts";
import { Card } from "@/components/ui/Card.tsx";
import { StatusBadge } from "@/components/ui/StatusBadge.tsx";
import { ErrorBanner } from "@/components/ui/ErrorBanner.tsx";

export default function IpamStatus() {
  const { data, loading, error, lastFetchedAt } = usePoll<CiliumIPAMResponse>(
    "/v1/networking/cilium/ipam",
    {
      interval: 60_000,
      shouldContinuePolling: (d) => d.configured,
    },
  );

  if (!IS_BROWSER || loading.value) {
    return <div class="animate-pulse h-40 bg-elevated rounded-lg" />;
  }

  if (error.value) {
    return (
      <Card title="IP Address Management">
        <ErrorBanner message={error.value} />
      </Card>
    );
  }

  const resp = data.value;
  if (!resp || !resp.configured) {
    return (
      <Card title="IP Address Management" class="opacity-50">
        <p class="text-sm text-text-muted">IPAM data not available.</p>
      </Card>
    );
  }

  const allocated = resp.allocated ?? 0;
  const total = resp.total ?? 0;

  const pct = total > 0 ? Math.round((allocated / total) * 100) : 0;

  const riskVariant = resp.exhaustionRisk === "high"
    ? "danger"
    : resp.exhaustionRisk === "medium"
    ? "warning"
    : "success";

  return (
    <Card title="IP Address Management">
      <div class="space-y-3">
        <div class="flex justify-between">
          <span class="text-text-muted">Mode</span>
          <span class="text-text-primary font-medium capitalize">
            {resp.mode.replace(/-/g, " ")}
          </span>
        </div>
        {resp.podCIDRs.length > 0 && (
          <div class="flex justify-between">
            <span class="text-text-muted">Pod CIDR</span>
            <span class="font-mono text-sm text-text-primary">
              {resp.podCIDRs[0]}
              {resp.podCIDRs.length > 1 && (
                <span class="text-text-muted">
                  &nbsp;+{resp.podCIDRs.length - 1}
                </span>
              )}
            </span>
          </div>
        )}
        <div class="flex justify-between">
          <span class="text-text-muted">Allocated</span>
          <span class="font-mono text-sm text-text-primary">
            {allocated.toLocaleString()} / {total.toLocaleString()}
          </span>
        </div>
        {/* Progress bar */}
        <div class="w-full bg-elevated rounded-full h-2">
          <div
            class="h-2 rounded-full"
            style={{
              width: `${Math.max(pct, 1)}%`,
              background: resp.exhaustionRisk === "high"
                ? "var(--error)"
                : resp.exhaustionRisk === "medium"
                ? "var(--warning)"
                : "var(--accent)",
              minWidth: allocated > 0 ? "4px" : "0",
            }}
          />
        </div>
        <div class="flex justify-between">
          <span class="text-text-muted">Exhaustion Risk</span>
          <StatusBadge
            status={resp.exhaustionRisk === "none"
              ? "None"
              : resp.exhaustionRisk.charAt(0).toUpperCase() +
                resp.exhaustionRisk.slice(1)}
            variant={riskVariant}
          />
        </div>

        {/* Per-node breakdown */}
        {resp.perNode.length > 0 && (
          <div class="mt-4 pt-3 border-t border-border-subtle">
            <p class="text-xs font-medium text-text-muted uppercase tracking-wider mb-2">
              Per Node
            </p>
            <div class="space-y-1.5">
              {resp.perNode.map((node) => (
                <div
                  key={node.node}
                  class="flex justify-between text-xs"
                >
                  <span class="font-mono text-text-secondary">
                    {node.node}
                  </span>
                  <span class="text-text-muted">
                    {node.allocated} used / {node.allocated + node.available}
                    {" "}
                    total
                  </span>
                </div>
              ))}
            </div>
          </div>
        )}
      </div>
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
