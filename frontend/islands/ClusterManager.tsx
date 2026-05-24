import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { useEffect } from "preact/hooks";
import { api, apiGet, apiPost } from "@/lib/api.ts";
import { showToast } from "@/islands/ToastProvider.tsx";
import { Button } from "@/components/ui/Button.tsx";
import { Input } from "@/components/ui/Input.tsx";
import { Card } from "@/components/ui/Card.tsx";
import { StatusBadge } from "@/components/ui/StatusBadge.tsx";
import { Spinner } from "@/components/ui/Spinner.tsx";
import { ErrorBanner } from "@/components/ui/ErrorBanner.tsx";

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
  // F#10 — surface the operator-opted-in insecure TLS state in the cluster
  // list so admins can spot homelab-only registrations at a glance and so
  // any unexpected `true` (e.g. after a CA-less rolling-upgrade backfill)
  // gets noticed before it's used to send bearer tokens over an unverified
  // connection. Optional because older backend versions omit the field.
  allowInsecureTLS?: boolean;
  lastProbedAt?: string;
}

type WizardStep = "list" | "connect";

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
  // F#2 — admin-only opt-in for unverified TLS. Bearer tokens travel over
  // the connection, so default-false. Backend re-validates admin role and
  // rejects registrations missing both CA and this opt-in.
  const allowInsecureTLS = useSignal(false);

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
          allowInsecureTLS: allowInsecureTLS.value,
        }),
      });
      // Reset wizard and reload
      step.value = "list";
      name.value = "";
      displayName.value = "";
      apiServerUrl.value = "";
      token.value = "";
      caCert.value = "";
      allowInsecureTLS.value = false;
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
        <Spinner class="text-accent" />
      </div>
    );
  }

  // Add Cluster Wizard
  if (step.value === "connect") {
    return (
      <Card title="Add Cluster">
        <div class="space-y-4">
          {error.value && <ErrorBanner message={error.value} />}

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
            <label class="mb-1 block text-sm font-medium text-text-secondary">
              Service Account Token
            </label>
            <textarea
              class="w-full rounded-md border border-border-primary px-3 py-2 text-sm font-mono bg-surface text-text-secondary"
              rows={3}
              placeholder="eyJhbGciOiJSUzI1NiIs..."
              value={token.value}
              onInput={(e) => {
                token.value = (e.target as HTMLTextAreaElement).value;
              }}
            />
          </div>
          <div>
            <label class="mb-1 block text-sm font-medium text-text-secondary">
              CA Certificate (optional with insecure TLS opt-in below)
            </label>
            <textarea
              class="w-full rounded-md border border-border-primary px-3 py-2 text-sm font-mono bg-surface text-text-secondary"
              rows={3}
              placeholder="-----BEGIN CERTIFICATE-----"
              value={caCert.value}
              onInput={(e) => {
                caCert.value = (e.target as HTMLTextAreaElement).value;
              }}
            />
          </div>
          {/* F#2 — admin-only opt-in for unverified TLS. Default-off. */}
          <div class="rounded-md border border-warning bg-warning-dim p-3 text-sm">
            <label class="flex items-start gap-2 text-text-primary cursor-pointer">
              <input
                type="checkbox"
                class="mt-0.5"
                checked={allowInsecureTLS.value}
                onChange={(e) => {
                  allowInsecureTLS.value =
                    (e.target as HTMLInputElement).checked;
                }}
              />
              <span>
                <span class="font-medium">
                  Allow insecure TLS (self-signed CA, homelab only)
                </span>
                <span class="block mt-1 text-xs text-warning">
                  Bearer tokens will be sent over an unverified TLS connection.
                  Only enable for trusted homelab self-signed certs. Admin role
                  required.
                </span>
              </span>
            </label>
          </div>
          <div class="flex gap-3">
            <Button
              type="button"
              variant="primary"
              loading={saving.value}
              onClick={addCluster}
              disabled={!name.value || !apiServerUrl.value || !token.value ||
                (!caCert.value && !allowInsecureTLS.value)}
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
        <p class="text-sm text-text-muted">
          {clusters.value.length}
          {""}
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

      <div class="divide-y divide-border-primary rounded-lg border border-border-primary">
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
                <p class="text-sm font-medium text-text-primary">
                  {c.displayName || c.name}
                  {c.isLocal && (
                    <span class="ml-2 rounded px-1.5 py-0.5 text-xs bg-accent-dim text-accent">
                      local
                    </span>
                  )}
                  {c.allowInsecureTLS && !c.isLocal && (
                    <span
                      // F#14 (round-3) — was bg-warning-dim/text-warning
                      // (amber). Insecure-TLS clusters leak bearer tokens
                      // over an unverified connection; amber under-sells
                      // the risk relative to a misconfigured-production
                      // worst case. Red/error tokens match the severity
                      // operators should perceive when scanning the list.
                      class="ml-2 rounded px-1.5 py-0.5 text-xs bg-error-dim text-error"
                      title="TLS verification disabled — bearer tokens are sent over an unverified connection"
                    >
                      ⚠ Insecure TLS
                    </span>
                  )}
                </p>
                <p class="text-xs text-text-muted">
                  {c.k8sVersion || "unknown"} &middot; {c.nodeCount}
                  {""}
                  nodes &middot; {c.apiServerUrl || c.id}
                  {c.lastProbedAt && (
                    <span>
                      {` · checked ${
                        new Date(c.lastProbedAt).toLocaleTimeString()
                      }`}
                    </span>
                  )}
                </p>
                {c.status !== "connected" && c.statusMessage && (
                  <p class="text-xs text-danger mt-0.5">{c.statusMessage}</p>
                )}
              </div>
            </div>
            <div class="flex items-center gap-2">
              <StatusBadge
                status={c.status === "connected" ? "running" : "failed"}
                label={c.status}
              />
              {!c.isLocal && (
                <>
                  <Button
                    type="button"
                    variant="secondary"
                    size="sm"
                    onClick={async () => {
                      try {
                        const res = await apiPost<ClusterInfo>(
                          `/v1/clusters/${c.id}/test`,
                        );
                        // Update the cluster in the list with fresh probe data
                        clusters.value = clusters.value.map((cl) =>
                          cl.id === c.id ? { ...cl, ...res.data } : cl
                        );
                        showToast(
                          res.data.status === "connected"
                            ? "Connection successful"
                            : "Connection failed",
                          res.data.status === "connected" ? "success" : "error",
                        );
                      } catch {
                        showToast("Test failed", "error");
                      }
                    }}
                  >
                    Test
                  </Button>
                  <Button
                    type="button"
                    variant="danger"
                    size="sm"
                    onClick={() => deleteCluster(c.id)}
                  >
                    Remove
                  </Button>
                </>
              )}
            </div>
          </div>
        ))}
        {clusters.value.length === 0 && (
          <p class="px-4 py-8 text-center text-sm text-text-muted">
            No clusters registered. Add a cluster to get started.
          </p>
        )}
      </div>
    </div>
  );
}
