/** @jsxImportSource react */
import { Card } from "@/components/ui/Card.tsx";
import StatusBadge from "@/components/ui/glass/StatusBadge.tsx";
import { PanelStateNotice } from "@/components/ui/PanelStateNotice.tsx";
import type { CiliumBGPResponse } from "@/lib/cilium-types.ts";
import { usePoll } from "@/lib/hooks/use-poll.ts";
import { panelState } from "@/lib/networking-state.ts";
import { useIsClient } from "@/src/lib/use-is-client.ts";

export default function BgpStatus() {
  const { data, loading, error, failure, refetch, lastFetchedAt } =
    usePoll<CiliumBGPResponse>("/v1/networking/cilium/bgp", {
      interval: 60_000,
      shouldContinuePolling: (d) => d.configured,
    });
  const isClient = useIsClient();

  if (!isClient || loading) {
    return <div className="animate-pulse h-40 bg-elevated rounded-lg" />;
  }

  if (error) {
    return (
      <Card title="BGP Peering">
        <PanelStateNotice
          state={panelState(failure, "BGP status")}
          onRetry={refetch}
        />
      </Card>
    );
  }

  const resp = data;
  if (!resp?.configured) {
    return (
      <Card title="BGP Peering" className="opacity-50">
        <p className="text-sm text-text-muted">BGP is not configured.</p>
      </Card>
    );
  }

  const peers = resp.peers;
  const established = peers.filter(
    (p) => p.sessionState === "established",
  ).length;
  const allEstablished = established === peers.length && peers.length > 0;

  return (
    <Card title="BGP Peering">
      <div className="flex justify-between items-center -mt-2 mb-3">
        <StatusBadge
          label={
            allEstablished
              ? "All Established"
              : `${established}/${peers.length} Established`
          }
          tone={allEstablished ? "ok" : established > 0 ? "warn" : "crit"}
        />
      </div>
      <div className="space-y-2">
        {peers.map((peer, i) => (
          // biome-ignore lint/suspicious/noArrayIndexKey: peers have no unique id, and the list is replaced whole on each poll
          <div key={i} className="flex items-center justify-between text-sm">
            <div className="flex items-center gap-2">
              <span
                className="w-2 h-2 rounded-full inline-block"
                style={{
                  background:
                    peer.sessionState === "established"
                      ? "var(--success)"
                      : peer.sessionState === "idle"
                        ? "var(--error)"
                        : "var(--warning)",
                }}
              />
              <span className="font-mono text-text-primary">
                {peer.peerAddress}
              </span>
              <span className="text-xs text-text-muted">AS {peer.peerASN}</span>
            </div>
            <div className="flex items-center gap-3 text-xs text-text-muted">
              <span>
                ↑{peer.routesAdvertised} ↓{peer.routesReceived}
              </span>
            </div>
          </div>
        ))}
        {peers.length === 0 && (
          <p className="text-sm text-text-muted">No BGP peers found.</p>
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
