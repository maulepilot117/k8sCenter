import { useSignal } from "@preact/signals";
import { useEffect, useRef } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiGet } from "@/lib/api.ts";
import { Card } from "@/components/ui/Card.tsx";
import { StatusBadge } from "@/components/ui/StatusBadge.tsx";
import { Button } from "@/components/ui/Button.tsx";
import { ErrorBanner } from "@/components/ui/ErrorBanner.tsx";

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
  detectionMethod: string;
}

const POLL_INTERVAL = 120_000;

export default function CniOverview() {
  const cniInfo = useSignal<CNIInfo | null>(null);
  const loading = useSignal(true);
  const refreshing = useSignal(false);
  const error = useSignal<string | null>(null);
  const intervalRef = useRef<number | null>(null);

  const fetchCNI = async (refresh = false) => {
    try {
      const url = refresh
        ? "/v1/networking/cni?refresh=true"
        : "/v1/networking/cni";
      const resp = await apiGet<CNIInfo>(url);
      cniInfo.value = resp.data;
      error.value = null;
    } catch (err) {
      error.value = err instanceof Error
        ? err.message
        : "Failed to fetch CNI status";
    }
  };

  useEffect(() => {
    if (!IS_BROWSER) return;
    loading.value = true;
    fetchCNI().finally(() => {
      loading.value = false;
    });

    intervalRef.current = setInterval(() => {
      if (document.hidden) return;
      fetchCNI();
    }, POLL_INTERVAL) as unknown as number;

    return () => {
      if (intervalRef.current !== null) clearInterval(intervalRef.current);
    };
  }, []);

  const handleRefresh = async () => {
    refreshing.value = true;
    await fetchCNI(true);
    refreshing.value = false;
  };

  if (!IS_BROWSER) {
    return (
      <>
        <div class="animate-pulse h-40 bg-elevated rounded-lg" />
        <div class="animate-pulse h-40 bg-elevated rounded-lg" />
      </>
    );
  }

  if (loading.value) {
    return (
      <>
        <div class="animate-pulse h-40 bg-elevated rounded-lg" />
        <div class="animate-pulse h-40 bg-elevated rounded-lg" />
      </>
    );
  }

  if (error.value) {
    return (
      <div class="md:col-span-2">
        <ErrorBanner message={error.value} />
      </div>
    );
  }

  const info = cniInfo.value;
  if (!info || info.name === "unknown") {
    return (
      <div class="md:col-span-2">
        <Card>
          <div class="p-6 text-center text-text-muted">
            <p class="text-lg font-medium">No CNI Plugin Detected</p>
            <p class="mt-2 text-sm">
              Could not detect a supported CNI plugin (Cilium, Calico, or
              Flannel).
            </p>
            <Button
              variant="secondary"
              onClick={handleRefresh}
              disabled={refreshing.value}
              class="mt-4"
            >
              {refreshing.value ? "Scanning..." : "Re-scan Cluster"}
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
        <div class="flex justify-end -mt-8 mb-2">
          <Button
            variant="ghost"
            onClick={handleRefresh}
            disabled={refreshing.value}
          >
            {refreshing.value ? "..." : "Refresh"}
          </Button>
        </div>
        <div class="space-y-3">
          <div class="flex justify-between">
            <span class="text-text-muted">Plugin</span>
            <span class="font-medium text-text-primary capitalize">
              {info.name}
            </span>
          </div>
          {info.version && (
            <div class="flex justify-between">
              <span class="text-text-muted">Version</span>
              <span class="font-mono text-sm">
                {info.version.split("@")[0].replace(/^.*:/, "")}
              </span>
            </div>
          )}
          <div class="flex justify-between">
            <span class="text-text-muted">Namespace</span>
            <span class="font-mono text-sm">{info.namespace || "N/A"}</span>
          </div>
          <div class="flex justify-between">
            <span class="text-text-muted">DaemonSet</span>
            <span class="font-mono text-sm">{info.daemonSet || "N/A"}</span>
          </div>
          <div class="flex justify-between">
            <span class="text-text-muted">CRDs</span>
            <StatusBadge
              status={info.hasCRDs ? "Installed" : "Not Found"}
              variant={info.hasCRDs ? "success" : "neutral"}
            />
          </div>
        </div>
      </Card>

      {/* Health card */}
      {info.status && info.status.desired > 0 && (
        <Card title="Health">
          <div class="space-y-3">
            <div class="flex justify-between items-center">
              <span class="text-text-muted">Status</span>
              <StatusBadge
                status={info.status.healthy ? "Healthy" : "Degraded"}
                variant={info.status.healthy ? "success" : "warning"}
              />
            </div>
            <div class="flex justify-between">
              <span class="text-text-muted">Ready Pods</span>
              <span class="font-mono text-sm">
                {info.status.ready} / {info.status.desired}
              </span>
            </div>
            <div class="w-full bg-elevated rounded-full h-2">
              <div
                class={`h-2 rounded-full ${
                  info.status.healthy ? "bg-success" : "bg-warning"
                }`}
                style={{
                  width: `${
                    Math.round(
                      (info.status.ready / info.status.desired) * 100,
                    )
                  }%`,
                }}
              />
            </div>
          </div>
        </Card>
      )}
    </>
  );
}
