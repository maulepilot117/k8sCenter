import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { useEffect } from "preact/hooks";
import { api, apiGet } from "@/lib/api.ts";
import { Button } from "@/components/ui/Button.tsx";
import { Input } from "@/components/ui/Input.tsx";
import { Card } from "@/components/ui/Card.tsx";
import { StatusBadge } from "@/components/ui/StatusBadge.tsx";

interface ClusterInfo {
  id: string;
  name: string;
  displayName: string;
  apiServerUrl: string;
  status: string;
  statusMessage: string;
  k8sVersion: string;
  nodeCount: number;
  isLocal: boolean;
}

type WizardStep = "list" | "connect" | "confirm";

/**
 * Cluster management island — list clusters + add cluster wizard.
 */
export default function ClusterManager() {
  const clusters = useSignal<ClusterInfo[]>([]);
  const loading = useSignal(true);
  const step = useSignal<WizardStep>("list");
  const error = useSignal("");
  const saving = useSignal(false);

  // Wizard form state
  const name = useSignal("");
  const displayName = useSignal("");
  const apiServerUrl = useSignal("");
  const token = useSignal("");
  const caCert = useSignal("");

  useEffect(() => {
    if (!IS_BROWSER) return;
    loadClusters();
  }, []);

  async function loadClusters() {
    loading.value = true;
    try {
      const res = await apiGet<ClusterInfo[]>("/v1/clusters");
      clusters.value = Array.isArray(res.data) ? res.data : [];
    } catch {
      // May not have database configured
      clusters.value = [];
    } finally {
      loading.value = false;
    }
  }

  async function addCluster() {
    error.value = "";
    saving.value = true;
    try {
      await api("/v1/clusters", {
        method: "POST",
        body: JSON.stringify({
          name: name.value,
          displayName: displayName.value,
          apiServerUrl: apiServerUrl.value,
          token: token.value,
          caCert: caCert.value,
        }),
      });
      // Reset wizard and reload
      step.value = "list";
      name.value = "";
      displayName.value = "";
      apiServerUrl.value = "";
      token.value = "";
      caCert.value = "";
      await loadClusters();
    } catch (err) {
      error.value = err instanceof Error
        ? err.message
        : "Failed to register cluster";
    } finally {
      saving.value = false;
    }
  }

  async function deleteCluster(id: string) {
    if (!confirm("Remove this cluster?")) return;
    try {
      await api(`/v1/clusters/${id}`, { method: "DELETE" });
      await loadClusters();
    } catch {
      // ignore
    }
  }

  if (loading.value) {
    return (
      <div class="flex justify-center py-12">
        <div class="h-6 w-6 animate-spin rounded-full border-2 border-slate-300 border-t-blue-600" />
      </div>
    );
  }

  // Add Cluster Wizard
  if (step.value === "connect") {
    return (
      <Card title="Add Cluster">
        <div class="space-y-4">
          {error.value && (
            <div class="rounded-md bg-red-50 px-4 py-3 text-sm text-red-800 dark:bg-red-900/30 dark:text-red-400">
              {error.value}
            </div>
          )}

          <Input
            label="Cluster Name"
            type="text"
            placeholder="production-us-east"
            value={name.value}
            onInput={(e) => {
              name.value = (e.target as HTMLInputElement).value;
            }}
            required
          />
          <Input
            label="Display Name (optional)"
            type="text"
            placeholder="Production US East"
            value={displayName.value}
            onInput={(e) => {
              displayName.value = (e.target as HTMLInputElement).value;
            }}
          />
          <Input
            label="API Server URL"
            type="url"
            placeholder="https://k8s-api.example.com:6443"
            value={apiServerUrl.value}
            onInput={(e) => {
              apiServerUrl.value = (e.target as HTMLInputElement).value;
            }}
            required
          />
          <div>
            <label class="mb-1 block text-sm font-medium text-slate-700 dark:text-slate-300">
              Service Account Token
            </label>
            <textarea
              class="w-full rounded-md border border-slate-300 px-3 py-2 text-sm font-mono dark:border-slate-600 dark:bg-slate-700 dark:text-slate-200"
              rows={3}
              placeholder="eyJhbGciOiJSUzI1NiIs..."
              value={token.value}
              onInput={(e) => {
                token.value = (e.target as HTMLTextAreaElement).value;
              }}
            />
          </div>
          <div>
            <label class="mb-1 block text-sm font-medium text-slate-700 dark:text-slate-300">
              CA Certificate (optional)
            </label>
            <textarea
              class="w-full rounded-md border border-slate-300 px-3 py-2 text-sm font-mono dark:border-slate-600 dark:bg-slate-700 dark:text-slate-200"
              rows={3}
              placeholder="-----BEGIN CERTIFICATE-----"
              value={caCert.value}
              onInput={(e) => {
                caCert.value = (e.target as HTMLTextAreaElement).value;
              }}
            />
          </div>
          <div class="flex gap-3">
            <Button
              type="button"
              variant="primary"
              loading={saving.value}
              onClick={addCluster}
              disabled={!name.value || !apiServerUrl.value || !token.value}
            >
              Register Cluster
            </Button>
            <Button
              type="button"
              variant="ghost"
              onClick={() => {
                step.value = "list";
              }}
            >
              Cancel
            </Button>
          </div>
        </div>
      </Card>
    );
  }

  // Cluster List
  return (
    <div class="space-y-4">
      <div class="flex items-center justify-between">
        <p class="text-sm text-slate-500 dark:text-slate-400">
          {clusters.value.length}{" "}
          cluster{clusters.value.length !== 1 ? "s" : ""} registered
        </p>
        <Button
          type="button"
          variant="primary"
          onClick={() => {
            step.value = "connect";
          }}
        >
          Add Cluster
        </Button>
      </div>

      <div class="divide-y divide-slate-200 rounded-lg border border-slate-200 dark:divide-slate-700 dark:border-slate-700">
        {clusters.value.map((c) => (
          <div
            key={c.id}
            class="flex items-center justify-between px-4 py-3"
          >
            <div class="flex items-center gap-3">
              <span
                class={`h-2.5 w-2.5 rounded-full ${
                  c.status === "connected" ? "bg-success" : "bg-danger"
                }`}
              />
              <div>
                <p class="text-sm font-medium text-slate-900 dark:text-white">
                  {c.displayName || c.name}
                  {c.isLocal && (
                    <span class="ml-2 rounded bg-blue-100 px-1.5 py-0.5 text-xs text-blue-700 dark:bg-blue-900/30 dark:text-blue-400">
                      local
                    </span>
                  )}
                </p>
                <p class="text-xs text-slate-500 dark:text-slate-400">
                  {c.k8sVersion || "unknown"} &middot; {c.nodeCount}{" "}
                  nodes &middot; {c.apiServerUrl || c.id}
                </p>
              </div>
            </div>
            <div class="flex items-center gap-2">
              <StatusBadge
                status={c.status === "connected" ? "running" : "failed"}
                label={c.status}
              />
              {!c.isLocal && (
                <Button
                  type="button"
                  variant="danger"
                  size="sm"
                  onClick={() => deleteCluster(c.id)}
                >
                  Remove
                </Button>
              )}
            </div>
          </div>
        ))}
        {clusters.value.length === 0 && (
          <p class="px-4 py-8 text-center text-sm text-slate-500 dark:text-slate-400">
            No clusters registered. Add a cluster to get started.
          </p>
        )}
      </div>
    </div>
  );
}
