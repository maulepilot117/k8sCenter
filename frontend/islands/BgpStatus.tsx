import { IS_BROWSER } from "fresh/runtime";
import { usePoll } from "@/lib/hooks/use-poll.ts";
import type { CiliumBGPResponse } from "@/lib/cilium-types.ts";
import { Card } from "@/components/ui/Card.tsx";
import { StatusBadge } from "@/components/ui/StatusBadge.tsx";
import { ErrorBanner } from "@/components/ui/ErrorBanner.tsx";

export default function BgpStatus() {
  const { data, loading, error } = usePoll<CiliumBGPResponse>(
    "/v1/networking/cilium/bgp",
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
      <Card title="BGP Peering">
        <ErrorBanner message={error.value} />
      </Card>
    );
  }

  const resp = data.value;
  if (!resp || !resp.configured) {
    return (
      <Card title="BGP Peering" class="opacity-50">
        <p class="text-sm text-text-muted">BGP is not configured.</p>
      </Card>
    );
  }

  const peers = resp.peers;
  const established =
    peers.filter((p) => p.sessionState === "established").length;
  const allEstablished = established === peers.length && peers.length > 0;

  return (
    <Card title="BGP Peering">
      <div class="flex justify-between items-center -mt-2 mb-3">
        <StatusBadge
          status={allEstablished
            ? "All Established"
            : `${established}/${peers.length} Established`}
          variant={allEstablished
            ? "success"
            : established > 0
            ? "warning"
            : "danger"}
        />
      </div>
      <div class="space-y-2">
        {peers.map((peer, i) => (
          <div
            key={i}
            class="flex items-center justify-between text-sm"
          >
            <div class="flex items-center gap-2">
              <span
                class="w-2 h-2 rounded-full inline-block"
                style={{
                  background: peer.sessionState === "established"
                    ? "var(--success)"
                    : peer.sessionState === "idle"
                    ? "var(--error)"
                    : "var(--warning)",
                }}
              />
              <span class="font-mono text-text-primary">
                {peer.peerAddress}
              </span>
              <span class="text-xs text-text-muted">
                AS {peer.peerASN}
              </span>
            </div>
            <div class="flex items-center gap-3 text-xs text-text-muted">
              <span>
                ↑{peer.routesAdvertised} ↓{peer.routesReceived}
              </span>
            </div>
          </div>
        ))}
        {peers.length === 0 && (
          <p class="text-sm text-text-muted">
            No BGP peers found.
          </p>
        )}
      </div>
    </Card>
  );
}
