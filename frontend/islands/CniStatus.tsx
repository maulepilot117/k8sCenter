import { useSignal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiGet, apiPut } from "@/lib/api.ts";
import { Button } from "@/components/ui/Button.tsx";
import { Card } from "@/components/ui/Card.tsx";
import { StatusBadge } from "@/components/ui/StatusBadge.tsx";

interface CNIFeatures {
  hubble: boolean;
  encryption: boolean;
  encryptionMode: string;
  clusterMesh: boolean;
  wireguard: boolean;
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

interface CiliumConfig {
  cniType: string;
  configSource: string;
  configMapName: string;
  configMapNamespace: string;
  editable: boolean;
  config: Record<string, string>;
}

export default function CniStatus() {
  const cniInfo = useSignal<CNIInfo | null>(null);
  const config = useSignal<CiliumConfig | null>(null);
  const loading = useSignal(true);
  const refreshing = useSignal(false);
  const error = useSignal<string | null>(null);
  const configTab = useSignal<"status" | "config">("status");

  const fetchCNI = async (refresh = false) => {
    try {
      const url = refresh
        ? "/v1/networking/cni?refresh=true"
        : "/v1/networking/cni";
      const resp = await apiGet<CNIInfo>(url);
      cniInfo.value = resp.data;
    } catch (err) {
      error.value = err instanceof Error
        ? err.message
        : "Failed to fetch CNI status";
    }
  };

  const fetchConfig = async () => {
    try {
      const resp = await apiGet<CiliumConfig | Record<string, unknown>>(
        "/v1/networking/cni/config",
      );
      if (resp.data && "config" in resp.data) {
        config.value = resp.data as CiliumConfig;
      } else {
        config.value = null;
      }
    } catch {
      // Config may not be available for all CNI types
    }
  };

  useEffect(() => {
    if (!IS_BROWSER) return;
    loading.value = true;
    Promise.all([fetchCNI(), fetchConfig()]).finally(() => {
      loading.value = false;
    });
  }, []);

  const handleRefresh = async () => {
    refreshing.value = true;
    await Promise.all([fetchCNI(true), fetchConfig()]);
    refreshing.value = false;
  };

  if (!IS_BROWSER) {
    return <div class="p-6">Loading CNI status...</div>;
  }

  if (loading.value) {
    return (
      <div class="p-6">
        <div class="animate-pulse space-y-4">
          <div class="h-8 bg-slate-200 dark:bg-slate-700 rounded w-48" />
          <div class="h-32 bg-slate-200 dark:bg-slate-700 rounded" />
        </div>
      </div>
    );
  }

  if (error.value) {
    return (
      <div class="p-6">
        <div class="bg-red-50 dark:bg-red-900/20 border border-red-200 dark:border-red-800 rounded-lg p-4 text-red-700 dark:text-red-300">
          {error.value}
        </div>
      </div>
    );
  }

  const info = cniInfo.value;
  if (!info || info.name === "unknown") {
    return (
      <div class="p-6">
        <h1 class="text-2xl font-bold text-slate-800 dark:text-white mb-4">
          CNI Plugin
        </h1>
        <Card>
          <div class="p-6 text-center text-slate-500 dark:text-slate-400">
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
    <div class="p-6">
      <div class="flex items-center justify-between mb-6">
        <h1 class="text-2xl font-bold text-slate-800 dark:text-white">
          CNI Plugin
        </h1>
        <Button
          variant="ghost"
          onClick={handleRefresh}
          disabled={refreshing.value}
        >
          {refreshing.value ? "Refreshing..." : "Refresh"}
        </Button>
      </div>

      {/* Tab navigation */}
      <div class="flex gap-1 mb-6 border-b border-slate-200 dark:border-slate-700">
        <button
          type="button"
          class={`px-4 py-2 text-sm font-medium border-b-2 -mb-px transition-colors ${
            configTab.value === "status"
              ? "border-brand text-brand"
              : "border-transparent text-slate-500 hover:text-slate-700 dark:hover:text-slate-300"
          }`}
          onClick={() => (configTab.value = "status")}
        >
          Status
        </button>
        {info.name === "cilium" && config.value?.editable && (
          <button
            type="button"
            class={`px-4 py-2 text-sm font-medium border-b-2 -mb-px transition-colors ${
              configTab.value === "config"
                ? "border-brand text-brand"
                : "border-transparent text-slate-500 hover:text-slate-700 dark:hover:text-slate-300"
            }`}
            onClick={() => (configTab.value = "config")}
          >
            Configuration
          </button>
        )}
      </div>

      {configTab.value === "status" && <CniStatusTab info={info} />}

      {configTab.value === "config" && config.value && (
        <CniConfigTab config={config.value} onUpdate={fetchConfig} />
      )}
    </div>
  );
}

function CniStatusTab({ info }: { info: CNIInfo }) {
  return (
    <div class="grid gap-6 md:grid-cols-2">
      {/* Overview */}
      <Card title="Overview">
        <div class="space-y-3">
          <div class="flex justify-between">
            <span class="text-slate-500 dark:text-slate-400">Plugin</span>
            <span class="font-medium text-slate-800 dark:text-white capitalize">
              {info.name}
            </span>
          </div>
          {info.version && (
            <div class="flex justify-between">
              <span class="text-slate-500 dark:text-slate-400">Version</span>
              <span class="font-mono text-sm">{info.version}</span>
            </div>
          )}
          <div class="flex justify-between">
            <span class="text-slate-500 dark:text-slate-400">Namespace</span>
            <span class="font-mono text-sm">
              {info.namespace || "N/A"}
            </span>
          </div>
          <div class="flex justify-between">
            <span class="text-slate-500 dark:text-slate-400">DaemonSet</span>
            <span class="font-mono text-sm">
              {info.daemonSet || "N/A"}
            </span>
          </div>
          <div class="flex justify-between">
            <span class="text-slate-500 dark:text-slate-400">Detection</span>
            <span class="text-sm">{info.detectionMethod}</span>
          </div>
          <div class="flex justify-between">
            <span class="text-slate-500 dark:text-slate-400">CRDs</span>
            <StatusBadge
              status={info.hasCRDs ? "Installed" : "Not Found"}
              variant={info.hasCRDs ? "success" : "neutral"}
            />
          </div>
        </div>
      </Card>

      {/* Health */}
      {info.status && info.status.desired > 0 && (
        <Card title="Health">
          <div class="space-y-3">
            <div class="flex justify-between items-center">
              <span class="text-slate-500 dark:text-slate-400">Status</span>
              <StatusBadge
                status={info.status.healthy ? "Healthy" : "Degraded"}
                variant={info.status.healthy ? "success" : "warning"}
              />
            </div>
            <div class="flex justify-between">
              <span class="text-slate-500 dark:text-slate-400">
                Ready Pods
              </span>
              <span class="font-mono text-sm">
                {info.status.ready} / {info.status.desired}
              </span>
            </div>
            {/* Progress bar */}
            <div class="w-full bg-slate-200 dark:bg-slate-700 rounded-full h-2">
              <div
                class={`h-2 rounded-full ${
                  info.status.healthy ? "bg-green-500" : "bg-amber-500"
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

      {/* Cilium Features */}
      {info.name === "cilium" && info.features && (
        <Card title="Features">
          <div class="space-y-3">
            <FeatureRow label="Hubble" enabled={info.features.hubble} />
            <FeatureRow
              label="Encryption"
              enabled={info.features.encryption}
              detail={info.features.encryptionMode}
            />
            <FeatureRow label="WireGuard" enabled={info.features.wireguard} />
            <FeatureRow
              label="Cluster Mesh"
              enabled={info.features.clusterMesh}
            />
          </div>
        </Card>
      )}
    </div>
  );
}

function FeatureRow(
  { label, enabled, detail }: {
    label: string;
    enabled: boolean;
    detail?: string;
  },
) {
  return (
    <div class="flex justify-between items-center">
      <span class="text-slate-500 dark:text-slate-400">{label}</span>
      <div class="flex items-center gap-2">
        {detail && enabled && (
          <span class="text-xs text-slate-400 dark:text-slate-500">
            {detail}
          </span>
        )}
        <span
          class={`inline-flex items-center px-2 py-0.5 rounded text-xs font-medium ${
            enabled
              ? "bg-green-100 text-green-700 dark:bg-green-900/30 dark:text-green-400"
              : "bg-slate-100 text-slate-500 dark:bg-slate-700 dark:text-slate-400"
          }`}
        >
          {enabled ? "Enabled" : "Disabled"}
        </span>
      </div>
    </div>
  );
}

function CniConfigTab(
  { config, onUpdate }: { config: CiliumConfig; onUpdate: () => void },
) {
  const editKey = useSignal<string | null>(null);
  const editValue = useSignal("");
  const saving = useSignal(false);
  const saveError = useSignal<string | null>(null);

  const startEdit = (key: string, value: string) => {
    editKey.value = key;
    editValue.value = value;
    saveError.value = null;
  };

  const cancelEdit = () => {
    editKey.value = null;
    editValue.value = "";
    saveError.value = null;
  };

  const saveEdit = async () => {
    if (editKey.value === null) return;
    saving.value = true;
    saveError.value = null;

    try {
      await apiPut("/v1/networking/cni/config", {
        changes: { [editKey.value]: editValue.value },
        confirmed: true,
      });
      editKey.value = null;
      editValue.value = "";
      onUpdate();
    } catch (err) {
      saveError.value = err instanceof Error
        ? err.message
        : "Failed to save configuration";
    } finally {
      saving.value = false;
    }
  };

  const sortedKeys = Object.keys(config.config).sort();

  return (
    <Card
      title={`Cilium Configuration (${config.configMapNamespace}/${config.configMapName})`}
    >
      {saveError.value && (
        <div class="mb-4 bg-red-50 dark:bg-red-900/20 border border-red-200 dark:border-red-800 rounded p-3 text-sm text-red-700 dark:text-red-300">
          {saveError.value}
        </div>
      )}
      <div class="overflow-x-auto">
        <table class="w-full text-sm">
          <thead>
            <tr class="border-b border-slate-200 dark:border-slate-700">
              <th class="text-left py-2 px-3 font-medium text-slate-500 dark:text-slate-400 w-1/3">
                Key
              </th>
              <th class="text-left py-2 px-3 font-medium text-slate-500 dark:text-slate-400">
                Value
              </th>
              <th class="py-2 px-3 w-20" />
            </tr>
          </thead>
          <tbody>
            {sortedKeys.map((key) => (
              <tr
                key={key}
                class="border-b border-slate-100 dark:border-slate-800 hover:bg-slate-50 dark:hover:bg-slate-800/50"
              >
                <td class="py-2 px-3 font-mono text-xs text-slate-700 dark:text-slate-300">
                  {key}
                </td>
                <td class="py-2 px-3">
                  {editKey.value === key
                    ? (
                      <input
                        type="text"
                        value={editValue.value}
                        onInput={(e) =>
                          editValue.value =
                            (e.target as HTMLInputElement).value}
                        class="w-full px-2 py-1 text-xs font-mono border border-brand rounded bg-white dark:bg-slate-800 dark:text-white"
                        onKeyDown={(e) => {
                          if (e.key === "Enter") saveEdit();
                          if (e.key === "Escape") cancelEdit();
                        }}
                      />
                    )
                    : (
                      <span class="font-mono text-xs text-slate-600 dark:text-slate-400">
                        {config.config[key] || (
                          <em class="text-slate-400">empty</em>
                        )}
                      </span>
                    )}
                </td>
                <td class="py-2 px-3 text-right">
                  {editKey.value === key
                    ? (
                      <div class="flex gap-1 justify-end">
                        <button
                          type="button"
                          onClick={saveEdit}
                          disabled={saving.value}
                          class="text-xs text-green-600 hover:text-green-700 font-medium"
                        >
                          {saving.value ? "..." : "Save"}
                        </button>
                        <button
                          type="button"
                          onClick={cancelEdit}
                          class="text-xs text-slate-400 hover:text-slate-600"
                        >
                          Cancel
                        </button>
                      </div>
                    )
                    : (
                      <button
                        type="button"
                        onClick={() => startEdit(key, config.config[key])}
                        class="text-xs text-brand hover:text-brand/80"
                      >
                        Edit
                      </button>
                    )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </Card>
  );
}
