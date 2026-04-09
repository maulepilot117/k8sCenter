import { IS_BROWSER } from "fresh/runtime";
import { usePoll } from "@/lib/hooks/use-poll.ts";
import type { CiliumSubsystemsResponse } from "@/lib/cilium-types.ts";
import { Card } from "@/components/ui/Card.tsx";
import { StatusBadge } from "@/components/ui/StatusBadge.tsx";
import { ErrorBanner } from "@/components/ui/ErrorBanner.tsx";

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  if (bytes < 1024 * 1024 * 1024) {
    return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
  }
  return `${(bytes / (1024 * 1024 * 1024)).toFixed(1)} GB`;
}

export default function CiliumSubsystems() {
  const { data, loading, error, lastFetchedAt } = usePoll<
    CiliumSubsystemsResponse
  >(
    "/v1/networking/cilium/subsystems",
    {
      interval: 60_000,
      shouldContinuePolling: (d) => d.configured,
    },
  );

  if (!IS_BROWSER || loading.value) {
    return <div class="animate-pulse h-32 bg-elevated rounded-lg" />;
  }

  if (error.value) {
    return (
      <Card title="Cilium Subsystems">
        <ErrorBanner message={error.value} />
      </Card>
    );
  }

  const resp = data.value;
  if (!resp || !resp.configured) {
    return (
      <Card title="Cilium Subsystems" class="opacity-50">
        <p class="text-sm text-text-muted">
          Subsystem data not available.
        </p>
      </Card>
    );
  }

  const { encryption, mesh, clusterMesh, endpoints } = resp;

  return (
    <Card title="Cilium Subsystems">
      <div class="grid gap-6 sm:grid-cols-2 lg:grid-cols-4">
        {/* Encryption */}
        <div>
          <p class="text-xs font-medium text-text-muted uppercase tracking-wider mb-2">
            Encryption
          </p>
          {encryption.enabled
            ? (
              <div class="space-y-1.5">
                <div class="flex justify-between text-sm">
                  <span class="text-text-muted">Mode</span>
                  <span class="font-medium text-text-primary capitalize">
                    {encryption.mode || "Unknown"}
                  </span>
                </div>
                <div class="flex justify-between text-sm">
                  <span class="text-text-muted">Nodes</span>
                  <span class="font-mono text-text-primary">
                    {encryption.nodesEncrypted} / {encryption.nodesTotal}
                  </span>
                </div>
                {encryption.wireGuardNodes &&
                  encryption.wireGuardNodes.length > 0 && (
                  <div class="mt-3 pt-2 border-t border-border-subtle">
                    <p class="text-xs text-text-muted mb-1.5">
                      WireGuard Peers
                    </p>
                    <div class="space-y-2">
                      {encryption.wireGuardNodes.map((wgn) => (
                        <div key={wgn.nodeName}>
                          <div class="flex justify-between text-xs">
                            <span class="font-mono text-text-secondary">
                              {wgn.nodeName}
                            </span>
                            <span class="text-text-muted">
                              {wgn.peerCount} peers
                            </span>
                          </div>
                          {wgn.peers.map((peer) => (
                            <div
                              key={peer.publicKey}
                              class="flex justify-between text-xs pl-3 mt-0.5"
                            >
                              <span class="font-mono text-text-muted">
                                {peer.endpoint}
                              </span>
                              <span class="text-text-muted">
                                ↓{formatBytes(peer.transferRx)}{" "}
                                ↑{formatBytes(peer.transferTx)}
                              </span>
                            </div>
                          ))}
                        </div>
                      ))}
                    </div>
                  </div>
                )}
              </div>
            )
            : <p class="text-sm text-text-muted">Disabled</p>}
        </div>

        {/* Service Mesh */}
        <div>
          <p class="text-xs font-medium text-text-muted uppercase tracking-wider mb-2">
            Service Mesh
          </p>
          {mesh.enabled
            ? (
              <div class="space-y-1.5">
                <div class="flex justify-between text-sm">
                  <span class="text-text-muted">Engine</span>
                  <StatusBadge
                    status={mesh.engine.charAt(0).toUpperCase() +
                      mesh.engine.slice(1)}
                    variant="success"
                  />
                </div>
                {mesh.deploymentMode && (
                  <div class="flex justify-between text-sm">
                    <span class="text-text-muted">Mode</span>
                    <span class="text-text-primary capitalize">
                      {mesh.deploymentMode}
                    </span>
                  </div>
                )}
                {mesh.totalRedirects != null && (
                  <div class="flex justify-between text-sm">
                    <span class="text-text-muted">Redirects</span>
                    <span class="font-mono text-text-primary">
                      {mesh.totalRedirects}
                    </span>
                  </div>
                )}
              </div>
            )
            : <p class="text-sm text-text-muted">Disabled</p>}
        </div>

        {/* ClusterMesh */}
        <div>
          <p class="text-xs font-medium text-text-muted uppercase tracking-wider mb-2">
            ClusterMesh
          </p>
          {clusterMesh.enabled
            ? (
              <div class="space-y-1.5">
                <StatusBadge status="Enabled" variant="success" />
                {clusterMesh.remoteClusters &&
                  clusterMesh.remoteClusters.length > 0 && (
                  <div class="mt-2 space-y-1">
                    {clusterMesh.remoteClusters.map((rc) => (
                      <div
                        key={rc.name}
                        class="flex items-center justify-between text-xs"
                      >
                        <div class="flex items-center gap-1.5">
                          <span
                            class="w-2 h-2 rounded-full inline-block"
                            style={{
                              background: rc.connected
                                ? "var(--success)"
                                : "var(--error)",
                            }}
                          />
                          <span class="font-mono text-text-secondary">
                            {rc.name}
                          </span>
                        </div>
                        <span class="text-text-muted">{rc.numNodes} nodes</span>
                      </div>
                    ))}
                  </div>
                )}
              </div>
            )
            : <p class="text-sm text-text-muted">Disabled</p>}
        </div>

        {/* Endpoints */}
        <div>
          <p class="text-xs font-medium text-text-muted uppercase tracking-wider mb-2">
            Endpoints
          </p>
          <div class="space-y-1.5">
            <div class="flex justify-between text-sm">
              <span class="text-text-muted">Total</span>
              <span class="font-mono font-medium text-text-primary">
                {endpoints.total}
              </span>
            </div>
            {/* State breakdown */}
            <div class="flex gap-3 text-xs">
              {endpoints.ready > 0 && (
                <span class="flex items-center gap-1">
                  <span
                    class="w-2 h-2 rounded-full inline-block"
                    style={{ background: "var(--success)" }}
                  />
                  {endpoints.ready}
                </span>
              )}
              {endpoints.notReady > 0 && (
                <span class="flex items-center gap-1">
                  <span
                    class="w-2 h-2 rounded-full inline-block"
                    style={{ background: "var(--warning)" }}
                  />
                  {endpoints.notReady}
                </span>
              )}
              {endpoints.disconnecting > 0 && (
                <span class="flex items-center gap-1">
                  <span
                    class="w-2 h-2 rounded-full inline-block"
                    style={{ background: "var(--error)" }}
                  />
                  {endpoints.disconnecting}
                </span>
              )}
              {endpoints.waiting > 0 && (
                <span class="flex items-center gap-1">
                  <span
                    class="w-2 h-2 rounded-full inline-block"
                    style={{ background: "var(--text-muted)" }}
                  />
                  {endpoints.waiting}
                </span>
              )}
            </div>
            {/* Stacked bar */}
            {endpoints.total > 0 && (
              <div class="flex h-1.5 rounded-full overflow-hidden bg-elevated mt-1">
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
                      width: `${
                        (endpoints.disconnecting / endpoints.total) * 100
                      }%`,
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
        </div>
      </div>
      {lastFetchedAt.value && (
        <div class="mt-4 pt-2 border-t border-border-subtle text-right">
          <span class="text-xs text-text-muted">
            Updated {lastFetchedAt.value.toLocaleTimeString()}
          </span>
        </div>
      )}
    </Card>
  );
}
