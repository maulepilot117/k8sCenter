import { Card } from "@/components/ui/Card.tsx";
import StatusBadge from "@/components/ui/glass/StatusBadge.tsx";
import { PanelStateNotice } from "@/components/ui/PanelStateNotice.tsx";
import { SourceCoverageNotice } from "@/components/ui/SourceCoverageNotice.tsx";
import type {
  CiliumSubsystemsResponse,
  EndpointCounts,
} from "@/lib/cilium-types.ts";
import { usePoll } from "@/lib/hooks/use-poll.ts";
import { panelState } from "@/lib/networking-state.ts";
import { useIsClient } from "@/src/lib/use-is-client.ts";

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  if (bytes < 1024 * 1024 * 1024) {
    return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
  }
  return `${(bytes / (1024 * 1024 * 1024)).toFixed(1)} GB`;
}

/** Display names for the sources a remote cluster may not provide. */
const COVERAGE_LABELS = {
  ciliumnodes: "Cilium nodes",
  ciliumendpoints: "Cilium endpoints",
  "cilium-agents": "Cilium agent data",
} as const;

export default function CiliumSubsystems() {
  const { data, loading, error, failure, refetch, lastFetchedAt } =
    usePoll<CiliumSubsystemsResponse>("/v1/networking/cilium/subsystems", {
      interval: 60_000,
      shouldContinuePolling: (d) => d.configured,
    });
  const isClient = useIsClient();

  if (!isClient || loading) {
    return <div className="animate-pulse h-32 bg-elevated rounded-lg" />;
  }

  if (error) {
    return (
      <Card title="Cilium Subsystems">
        <PanelStateNotice
          state={panelState(failure, "Cilium subsystem status")}
          onRetry={refetch}
        />
      </Card>
    );
  }

  const resp = data;
  if (!resp?.configured) {
    return (
      <Card title="Cilium Subsystems" className="opacity-50">
        <p className="text-sm text-text-muted">Subsystem data not available.</p>
      </Card>
    );
  }

  const { encryption, mesh, clusterMesh, endpoints, coverage } = resp;
  // Encryption node counts come from the CiliumNode list; when a remote
  // cluster could not provide it, the counts are unknown, not zero.
  const nodesUnknown =
    coverage?.some((c) => c.source === "ciliumnodes") ?? false;

  return (
    <Card title="Cilium Subsystems">
      <SourceCoverageNotice coverage={coverage} labels={COVERAGE_LABELS} />
      <div className="grid gap-6 sm:grid-cols-2 lg:grid-cols-4">
        {/* Encryption */}
        <div>
          <p className="text-xs font-medium text-text-muted uppercase tracking-wider mb-2">
            Encryption
          </p>
          {encryption.enabled ? (
            <div className="space-y-1.5">
              <div className="flex justify-between text-sm">
                <span className="text-text-muted">Mode</span>
                <span className="font-medium text-text-primary capitalize">
                  {encryption.mode || "Unknown"}
                </span>
              </div>
              <div className="flex justify-between text-sm">
                <span className="text-text-muted">Nodes</span>
                <span className="font-mono text-text-primary">
                  {nodesUnknown
                    ? "Not available"
                    : `${encryption.nodesEncrypted} / ${encryption.nodesTotal}`}
                </span>
              </div>
              {encryption.wireGuardNodes &&
                encryption.wireGuardNodes.length > 0 && (
                  <div className="mt-3 pt-2 border-t border-border-subtle">
                    <p className="text-xs text-text-muted mb-1.5">
                      WireGuard Peers
                    </p>
                    <div className="space-y-2">
                      {encryption.wireGuardNodes.map((wgn) => (
                        <div key={wgn.nodeName}>
                          <div className="flex justify-between text-xs">
                            <span className="font-mono text-text-secondary">
                              {wgn.nodeName}
                            </span>
                            <span className="text-text-muted">
                              {wgn.peerCount} peers
                            </span>
                          </div>
                          {wgn.peers.map((peer) => (
                            <div
                              key={peer.publicKey}
                              className="flex justify-between text-xs pl-3 mt-0.5"
                            >
                              <span className="font-mono text-text-muted">
                                {peer.endpoint}
                              </span>
                              <span className="text-text-muted">
                                ↓{formatBytes(peer.transferRx)} ↑
                                {formatBytes(peer.transferTx)}
                              </span>
                            </div>
                          ))}
                        </div>
                      ))}
                    </div>
                  </div>
                )}
            </div>
          ) : (
            <p className="text-sm text-text-muted">Disabled</p>
          )}
        </div>

        {/* Service Mesh */}
        <div>
          <p className="text-xs font-medium text-text-muted uppercase tracking-wider mb-2">
            Service Mesh
          </p>
          {mesh.enabled ? (
            <div className="space-y-1.5">
              <div className="flex justify-between text-sm">
                <span className="text-text-muted">Engine</span>
                <StatusBadge
                  label={
                    mesh.engine.charAt(0).toUpperCase() + mesh.engine.slice(1)
                  }
                  tone="ok"
                />
              </div>
              {mesh.deploymentMode && (
                <div className="flex justify-between text-sm">
                  <span className="text-text-muted">Mode</span>
                  <span className="text-text-primary capitalize">
                    {mesh.deploymentMode}
                  </span>
                </div>
              )}
              {mesh.totalRedirects != null && (
                <div className="flex justify-between text-sm">
                  <span className="text-text-muted">Redirects</span>
                  <span className="font-mono text-text-primary">
                    {mesh.totalRedirects}
                  </span>
                </div>
              )}
            </div>
          ) : (
            <p className="text-sm text-text-muted">Disabled</p>
          )}
        </div>

        {/* ClusterMesh */}
        <div>
          <p className="text-xs font-medium text-text-muted uppercase tracking-wider mb-2">
            ClusterMesh
          </p>
          {clusterMesh.enabled ? (
            <div className="space-y-1.5">
              <StatusBadge label="Enabled" tone="ok" />
              {clusterMesh.remoteClusters &&
                clusterMesh.remoteClusters.length > 0 && (
                  <div className="mt-2 space-y-1">
                    {clusterMesh.remoteClusters.map((rc) => (
                      <div
                        key={rc.name}
                        className="flex items-center justify-between text-xs"
                      >
                        <div className="flex items-center gap-1.5">
                          <span
                            className="w-2 h-2 rounded-full inline-block"
                            style={{
                              background: rc.connected
                                ? "var(--success)"
                                : "var(--error)",
                            }}
                          />
                          <span className="font-mono text-text-secondary">
                            {rc.name}
                          </span>
                        </div>
                        <span className="text-text-muted">
                          {rc.numNodes} nodes
                        </span>
                      </div>
                    ))}
                  </div>
                )}
            </div>
          ) : (
            <p className="text-sm text-text-muted">Disabled</p>
          )}
        </div>

        {/* Endpoints */}
        <div>
          <p className="text-xs font-medium text-text-muted uppercase tracking-wider mb-2">
            Endpoints
          </p>
          {endpoints ? (
            <EndpointsSummary endpoints={endpoints} />
          ) : (
            <p className="text-sm text-text-muted">Not available</p>
          )}
        </div>
      </div>
      {lastFetchedAt && (
        <div className="mt-4 pt-2 border-t border-border-subtle text-right">
          <span className="text-xs text-text-muted">
            Updated {lastFetchedAt.toLocaleTimeString()}
          </span>
        </div>
      )}
    </Card>
  );
}

/** Endpoint counts by state, with a stacked bar. */
function EndpointsSummary({ endpoints }: { endpoints: EndpointCounts }) {
  return (
    <div className="space-y-1.5">
      <div className="flex justify-between text-sm">
        <span className="text-text-muted">Total</span>
        <span className="font-mono font-medium text-text-primary">
          {endpoints.total}
        </span>
      </div>
      {/* State breakdown */}
      <div className="flex gap-3 text-xs">
        {endpoints.ready > 0 && (
          <span className="flex items-center gap-1">
            <span
              className="w-2 h-2 rounded-full inline-block"
              style={{ background: "var(--success)" }}
            />
            {endpoints.ready}
          </span>
        )}
        {endpoints.notReady > 0 && (
          <span className="flex items-center gap-1">
            <span
              className="w-2 h-2 rounded-full inline-block"
              style={{ background: "var(--warning)" }}
            />
            {endpoints.notReady}
          </span>
        )}
        {endpoints.disconnecting > 0 && (
          <span className="flex items-center gap-1">
            <span
              className="w-2 h-2 rounded-full inline-block"
              style={{ background: "var(--error)" }}
            />
            {endpoints.disconnecting}
          </span>
        )}
        {endpoints.waiting > 0 && (
          <span className="flex items-center gap-1">
            <span
              className="w-2 h-2 rounded-full inline-block"
              style={{ background: "var(--text-muted)" }}
            />
            {endpoints.waiting}
          </span>
        )}
      </div>
      {/* Stacked bar */}
      {endpoints.total > 0 && (
        <div className="flex h-1.5 rounded-full overflow-hidden bg-elevated mt-1">
          {endpoints.ready > 0 && (
            <div
              style={{
                width: `${(endpoints.ready / endpoints.total) * 100}%`,
                background: "var(--success)",
              }}
            />
          )}
          {endpoints.notReady > 0 && (
            <div
              style={{
                width: `${(endpoints.notReady / endpoints.total) * 100}%`,
                background: "var(--warning)",
              }}
            />
          )}
          {endpoints.disconnecting > 0 && (
            <div
              style={{
                width: `${(endpoints.disconnecting / endpoints.total) * 100}%`,
                background: "var(--error)",
              }}
            />
          )}
          {endpoints.waiting > 0 && (
            <div
              style={{
                width: `${(endpoints.waiting / endpoints.total) * 100}%`,
                background: "var(--text-muted)",
              }}
            />
          )}
        </div>
      )}
    </div>
  );
}
