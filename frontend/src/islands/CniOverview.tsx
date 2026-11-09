/** @jsxImportSource react */
import { useState } from "react";
import { Button } from "@/components/ui/Button.tsx";
import { Card } from "@/components/ui/Card.tsx";
import StatusBadge from "@/components/ui/glass/StatusBadge.tsx";
import { PanelStateNotice } from "@/components/ui/PanelStateNotice.tsx";
import { apiGet } from "@/lib/api.ts";
import { usePoll } from "@/lib/hooks/use-poll.ts";
import { panelState } from "@/lib/networking-state.ts";
import { useIsClient } from "@/src/lib/use-is-client.ts";

interface CNIFeatures {
  hubble: boolean;
  encryption: boolean;
  encryptionMode: string;
  clusterMesh: boolean;
  wireguard: boolean;
  envoyEnabled: boolean;
}

interface CNIStatus {
  ready: number;
  desired: number;
  healthy: boolean;
}

interface CNIInfo {
  name: string;
  version: string;
  namespace: string;
  daemonSet: string;
  status: CNIStatus;
  features: CNIFeatures;
  hasCRDs: boolean;
  /**
   * Set when a remote cluster's discovery could not be read, so hasCRDs
   * false means "unknown", not "absent". Never set on the local cluster.
   */
  crdsUnknown?: boolean;
  detectionMethod: string;
}

export default function CniOverview() {
  const {
    data: cniData,
    loading,
    error,
    failure,
    refetch,
  } = usePoll<CNIInfo>("/v1/networking/cni", { interval: 120_000 });
  const [refreshing, setRefreshing] = useState(false);
  const isClient = useIsClient();

  const handleRefresh = async () => {
    setRefreshing(true);
    try {
      await apiGet<CNIInfo>("/v1/networking/cni?refresh=true");
    } catch {
      /* ignore — next poll cycle picks up refreshed data */
    }
    setRefreshing(false);
  };

  if (!isClient || loading) {
    return (
      <>
        <div className="animate-pulse h-40 bg-elevated rounded-lg" />
        <div className="animate-pulse h-40 bg-elevated rounded-lg" />
      </>
    );
  }

  if (error) {
    return (
      <div className="md:col-span-2">
        <Card>
          <PanelStateNotice
            state={panelState(failure, "the CNI status")}
            onRetry={refetch}
          />
        </Card>
      </div>
    );
  }

  const info = cniData;
  if (!info || info.name === "unknown") {
    return (
      <div className="md:col-span-2">
        <Card>
          <div className="p-6 text-center text-text-muted">
            <p className="text-lg font-medium">No CNI Plugin Detected</p>
            <p className="mt-2 text-sm">
              Could not detect a supported CNI plugin (Cilium, Calico, or
              Flannel).
            </p>
            <Button
              variant="secondary"
              onClick={handleRefresh}
              disabled={refreshing}
              className="mt-4"
            >
              {refreshing ? "Scanning..." : "Re-scan Cluster"}
            </Button>
          </div>
        </Card>
      </div>
    );
  }

  return (
    <>
      {/* Overview card */}
      <Card title="Overview">
        <div className="flex justify-end -mt-8 mb-2">
          <Button variant="ghost" onClick={handleRefresh} disabled={refreshing}>
            {refreshing ? "..." : "Refresh"}
          </Button>
        </div>
        <div className="space-y-3">
          <div className="flex justify-between">
            <span className="text-text-muted">Plugin</span>
            <span className="font-medium text-text-primary capitalize">
              {info.name}
            </span>
          </div>
          {info.version && (
            <div className="flex justify-between">
              <span className="text-text-muted">Version</span>
              <span className="font-mono text-sm">
                {info.version.split("@")[0].replace(/^.*:/, "")}
              </span>
            </div>
          )}
          <div className="flex justify-between">
            <span className="text-text-muted">Namespace</span>
            <span className="font-mono text-sm">{info.namespace || "N/A"}</span>
          </div>
          <div className="flex justify-between">
            <span className="text-text-muted">DaemonSet</span>
            <span className="font-mono text-sm">{info.daemonSet || "N/A"}</span>
          </div>
          <div className="flex justify-between">
            <span className="text-text-muted">CRDs</span>
            <StatusBadge
              label={
                info.hasCRDs
                  ? "Installed"
                  : info.crdsUnknown
                    ? "Unknown"
                    : "Not Found"
              }
              tone={info.hasCRDs ? "ok" : "neutral"}
            />
          </div>
        </div>
      </Card>

      {/* Health card */}
      {info.status && info.status.desired > 0 && (
        <Card title="Health">
          <div className="space-y-3">
            <div className="flex justify-between items-center">
              <span className="text-text-muted">Status</span>
              <StatusBadge
                label={info.status.healthy ? "Healthy" : "Degraded"}
                tone={info.status.healthy ? "ok" : "warn"}
              />
            </div>
            <div className="flex justify-between">
              <span className="text-text-muted">Ready Pods</span>
              <span className="font-mono text-sm">
                {info.status.ready} / {info.status.desired}
              </span>
            </div>
            <div className="w-full bg-elevated rounded-full h-2">
              <div
                className={`h-2 rounded-full ${
                  info.status.healthy ? "bg-success" : "bg-warning"
                }`}
                style={{
                  width: `${Math.round(
                    (info.status.ready / info.status.desired) * 100,
                  )}%`,
                }}
              />
            </div>
          </div>
        </Card>
      )}
    </>
  );
}
